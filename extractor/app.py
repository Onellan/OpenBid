#!/usr/bin/env python3
import ipaddress, json, os, re, socket, subprocess, tempfile, urllib.parse, urllib.request
from datetime import datetime
from http.server import BaseHTTPRequestHandler, HTTPServer

EMAIL_RE=re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}")
PHONE_RE=re.compile(r"(?:\+27|0)[0-9][0-9 \-()/]{7,}")
MONTH_FORMATS=["%d %B %Y","%d %b %Y","%d/%m/%Y","%d-%m-%Y","%Y-%m-%d"]
MAX_FETCH_BYTES=15*1024*1024

def is_public_ip(value:str)->bool:
    ip=ipaddress.ip_address(value)
    return not (ip.is_private or ip.is_loopback or ip.is_link_local or ip.is_multicast or ip.is_unspecified or ip.is_reserved)

def validate_public_url(raw:str)->str:
    parsed=urllib.parse.urlparse((raw or "").strip())
    if parsed.scheme not in ("http","https"):
        raise ValueError("url must use http or https")
    if parsed.username or parsed.password:
        raise ValueError("url credentials are not allowed")
    host=(parsed.hostname or "").strip().lower()
    if not host:
        raise ValueError("url host is required")
    if host=="localhost" or host.endswith(".local") or host.endswith(".internal") or host.endswith(".localhost"):
        raise ValueError("private hostnames are not allowed")
    try:
        if not is_public_ip(host):
            raise ValueError("private or local network urls are not allowed")
    except ValueError:
        addresses={item[4][0] for item in socket.getaddrinfo(host,None)}
        if not addresses:
            raise ValueError("url host did not resolve")
        for address in addresses:
            if not is_public_ip(address):
                raise ValueError("private or local network urls are not allowed")
    return parsed.geturl()

def fetch(url:str)->bytes:
    safe_url=validate_public_url(url)
    req=urllib.request.Request(safe_url, headers={"User-Agent":"OpenBid/1.0"})
    with urllib.request.urlopen(req, timeout=60) as resp:
        validate_public_url(resp.geturl())
        content_length=resp.headers.get("Content-Length","").strip()
        if content_length.isdigit() and int(content_length)>MAX_FETCH_BYTES:
            raise ValueError("document is too large to extract safely")
        data=resp.read(MAX_FETCH_BYTES+1)
        if len(data)>MAX_FETCH_BYTES:
            raise ValueError("document is too large to extract safely")
        return data
def parse_pdf(data:bytes)->str:
    with tempfile.NamedTemporaryFile(delete=False,suffix=".pdf") as f: f.write(data); src=f.name
    txt=src+".txt"
    try:
        subprocess.run(["pdftotext","-layout",src,txt],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        with open(txt,"r",encoding="utf-8",errors="ignore") as fh: return fh.read()
    finally:
        for p in (src,txt):
            try: os.remove(p)
            except FileNotFoundError: pass
def parse_html(data:bytes)->str:
    text=data.decode("utf-8",errors="ignore")
    text=re.sub(r"<script.*?</script>"," ",text,flags=re.S|re.I)
    text=re.sub(r"<style.*?</style>"," ",text,flags=re.S|re.I)
    text=re.sub(r"<[^>]+>"," ",text)
    return re.sub(r"\s+"," ",text).strip()

def squash(text:str)->str:
    return re.sub(r"\s+"," ",text).strip()

def parse_date(value:str):
    value=squash(value)
    if not value:
        return None
    for candidate in [value, value.title(), value.upper()]:
        for fmt in MONTH_FORMATS:
            try: return datetime.strptime(candidate, fmt)
            except ValueError: pass
    return None

def normalize_date(value:str)->str:
    parsed=parse_date(value)
    return parsed.strftime("%Y-%m-%d") if parsed else squash(value)

def normalize_time(value:str)->str:
    value=squash(value).replace("H",":").replace("h",":")
    m=re.match(r"(?i)^(\d{1,2}):(\d{2})(?:\s*([ap]m))?$",value)
    if not m: return value
    hour=int(m.group(1)); minute=int(m.group(2)); suffix=(m.group(3) or "").lower()
    if suffix=="pm" and hour<12: hour+=12
    if suffix=="am" and hour==12: hour=0
    return f"{hour:02d}:{minute:02d}"

def normalize_datetime(date_value:str,time_value:str="")->str:
    parsed=parse_date(date_value)
    if not parsed: return squash(f"{date_value} {time_value}")
    time_value=normalize_time(time_value)
    if time_value and re.match(r"^\d{2}:\d{2}$",time_value):
        return f"{parsed.strftime('%Y-%m-%d')} {time_value}"
    return parsed.strftime("%Y-%m-%d")

def capture(text:str, patterns:list[str]):
    for pattern in patterns:
        m=re.search(pattern,text,flags=re.I|re.S|re.M)
        if m: return m
    return None

def headline(text:str)->str:
    lines=[squash(line) for line in text.splitlines() if squash(line)]
    chosen=[]
    for line in lines[:8]:
        if len(line)<8: continue
        if re.match(r"^(description of the service|date of issuance|closing date and time|rfb number|telephone number|contact persons|table of contents|page\s+\|)", line, flags=re.I): break
        chosen.append(line)
        if len(" ".join(chosen))>120: break
    return squash(" ".join(chosen[:3]))

def mine(text:str)->dict:
    lower=text.lower(); facts={}; pats={"closing_details":r"(closing[^.\n]{0,160})","briefing_details":r"(briefing[^.\n]{0,160})","submission_details":r"(submission[^.\n]{0,160})","contact_details":r"((?:email|tel|contact)[^.\n]{0,160})","cidb_hints":r"((?:cidb|grading)[^.\n]{0,160})"}
    for k,p in pats.items():
        m=re.search(p,text,flags=re.I)
        if m: facts[k]=squash(m.group(1))[:240]

    title=headline(text)
    if title: facts["document_title"]=title[:240]

    m=capture(text, [
        r"CLOSING DATE:\s*([0-9]{1,2}\s+[A-Za-z]+\s+[0-9]{4})\s*CLOSING TIME:\s*([0-9]{1,2}:\d{2})",
        r"closing(?: date(?: and time| on tender)?| time)?\s*[:\-]?\s*([0-9]{1,2}[\/-][0-9]{1,2}[\/-][0-9]{2,4}|[0-9]{1,2}\s+[A-Za-z]+\s+[0-9]{4})(?:\s*(?:at)?\s*([0-9]{1,2}(?::|H)\d{2}(?:\s*[ap]m)?))?",
    ])
    if m:
        facts["closing_date"]=normalize_date(m.group(1))
        if len(m.groups())>1 and m.group(2):
            facts["closing_time"]=normalize_time(m.group(2))
            facts["closing_datetime"]=normalize_datetime(m.group(1), m.group(2))

    m=capture(text, [r"(?:date of issuance|date published|date advertised)\s*[:\-]?\s*([0-9]{1,2}[\/-][0-9]{1,2}[\/-][0-9]{2,4}|[0-9]{1,2}\s+[A-Za-z]+\s+[0-9]{4})"])
    if m: facts["issued_date"]=normalize_date(m.group(1))

    m=capture(text, [r"(?:validity(?: period)?|proposal validity)[^0-9]{0,20}(\d+)\s*days", r"remain valid[^0-9]{0,20}(\d+)\s*days"])
    if m: facts["validity_days"]=m.group(1)

    m=capture(text, [r"\b(80/20|90/10)\b"])
    if m:
        facts["evaluation_method"]=m.group(1)
        parts=m.group(1).split("/")
        if len(parts)==2:
            facts["price_points"]=parts[0]
            facts["preference_points"]=parts[1]

    m=capture(text, [r"minimum\s+functionality(?:\s+score)?[^0-9%]{0,30}(\d+(?:\.\d+)?)\s*%", r"minimum threshold of\s+(\d+(?:\.\d+)?)\s*points"])
    if m: facts["minimum_functionality_score"]=m.group(1)

    m=capture(text, [r"\bCIDB\b[^.\n]{0,80}\b(\d+\s*[A-Z]{1,3})\b"])
    if m: facts["cidb_grade"]=squash(m.group(1)).replace(" ","")

    emails=EMAIL_RE.findall(text)
    if emails: facts["contact_email"]=emails[0]
    phones=[squash(m.group(0)) for m in PHONE_RE.finditer(text)]
    if phones: facts["contact_phone"]=phones[0]

    m=capture(text, [
        r"CONTACT PERSONS?\s+([A-Z][A-Za-z .'\-]{3,80})\s+(?:\+27|0)[0-9][0-9 \-()/]{7,}\s+[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}",
        r"All enquiries may be directed to:\s+([A-Z][A-Za-z .'\-]{3,80})",
        r"Contact(?: person| persons?)?\s*[:\-]?\s*([A-Z][A-Za-z .'\-]{3,80})",
    ])
    if m: facts["contact_name"]=squash(m.group(1))

    if re.search(r"non[- ]compulsory briefing session\s*none", text, flags=re.I):
        facts["briefing_required"]="no"
    m=capture(text, [
        r"(?:site briefing|briefing session|clarification meeting|site inspection)[^.\n]{0,80}on\s+([0-9]{1,2}\s+[A-Za-z]+\s+[0-9]{4})(?:\s+at\s+([0-9]{1,2}(?::|H)\d{2}(?:\s*[ap]m)?))?(?:\s+at\s+([^\n.]{5,120}))?",
        r"site briefing:\s*([0-9]{1,2}\s+[A-Za-z]+\s+[0-9]{4})(?:\s+at\s+([0-9]{1,2}:\d{2}\s*[ap]m))?(?:\s+at\s+([^\n.]{5,120}))?",
    ])
    if m:
        facts["briefing_required"]=facts.get("briefing_required","yes")
        facts["briefing_date"]=normalize_date(m.group(1))
        if len(m.groups())>1 and m.group(2):
            facts["briefing_time"]=normalize_time(m.group(2))
            facts["briefing_datetime"]=normalize_datetime(m.group(1), m.group(2))
        if len(m.groups())>2 and m.group(3):
            facts["briefing_venue"]=squash(m.group(3))

    m=capture(text, [
        r"Physical street address\s+([^\n]+)\s+City and Province\s+([^\n]+)",
        r"deposited in the (?:tender )?box at:\s*(.+?)\s+not later than",
    ])
    if m:
        address=squash(" ".join([g for g in m.groups() if g]))
        facts["submission_address"]=address[:240]

    m=capture(text, [r"City and Province\s+([A-Za-z ]+),\s*([A-Za-z ]+)"])
    if m:
        facts["location_city"]=squash(m.group(1))
        facts["location_province"]=squash(m.group(2))

    m=capture(text, [r"(?:Time of completion for this contract is|period of)\s+(\d+)\s*(weeks?|months?|years?)"])
    if m: facts["contract_duration"]=f"{m.group(1)} {m.group(2)}"

    return facts
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path=="/healthz":
            body=b'{"ok":true}'; self.send_response(200); self.send_header("Content-Type","application/json"); self.send_header("Content-Length",str(len(body))); self.end_headers(); self.wfile.write(body); return
        self.send_response(404); self.end_headers()
    def do_POST(self):
        if self.path!="/extract": self.send_response(404); self.end_headers(); return
        length=int(self.headers.get("Content-Length","0"))
        if length<=0 or length>4096:
            self.send_response(400); self.end_headers(); return
        payload=json.loads(self.rfile.read(length) or b"{}"); url=payload.get("url","")
        if not url: self.send_response(400); self.end_headers(); return
        try:
            data=fetch(url)
        except Exception as exc:
            body=json.dumps({"error":str(exc)}).encode()
            self.send_response(400)
            self.send_header("Content-Type","application/json")
            self.send_header("Content-Length",str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if url.lower().endswith(".pdf"): kind,text="pdf",parse_pdf(data)
        elif "<html" in data[:500].decode("utf-8",errors="ignore").lower(): kind,text="html",parse_html(data)
        else: kind,text="text",data.decode("utf-8",errors="ignore")
        out=json.dumps({"type":kind,"excerpt":text[:600],"facts":mine(text)}).encode()
        self.send_response(200); self.send_header("Content-Type","application/json"); self.send_header("Content-Length",str(len(out))); self.end_headers(); self.wfile.write(out)
if __name__=="__main__": HTTPServer(("0.0.0.0",int(os.getenv("PORT","9090"))),Handler).serve_forever()
