#!/usr/bin/env python3
import json, os, re, subprocess, tempfile, urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer
def fetch(url:str)->bytes:
    req=urllib.request.Request(url, headers={"User-Agent":"TenderHubZA/1.0"})
    with urllib.request.urlopen(req, timeout=60) as resp: return resp.read()
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
def mine(text:str)->dict:
    lower=text.lower(); facts={}; pats={"closing_details":r"(closing[^.\n]{0,160})","briefing_details":r"(briefing[^.\n]{0,160})","submission_details":r"(submission[^.\n]{0,160})","contact_details":r"((?:email|tel|contact)[^.\n]{0,160})","cidb_hints":r"((?:cidb|grading)[^.\n]{0,160})"}
    for k,p in pats.items():
        m=re.search(p,lower,flags=re.I)
        if m: facts[k]=m.group(1)[:200]
    return facts
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path=="/healthz":
            body=b'{"ok":true}'; self.send_response(200); self.send_header("Content-Type","application/json"); self.send_header("Content-Length",str(len(body))); self.end_headers(); self.wfile.write(body); return
        self.send_response(404); self.end_headers()
    def do_POST(self):
        if self.path!="/extract": self.send_response(404); self.end_headers(); return
        length=int(self.headers.get("Content-Length","0")); payload=json.loads(self.rfile.read(length) or b"{}"); url=payload.get("url","")
        if not url: self.send_response(400); self.end_headers(); return
        data=fetch(url)
        if url.lower().endswith(".pdf"): kind,text="pdf",parse_pdf(data)
        elif "<html" in data[:500].decode("utf-8",errors="ignore").lower(): kind,text="html",parse_html(data)
        else: kind,text="text",data.decode("utf-8",errors="ignore")
        out=json.dumps({"type":kind,"excerpt":text[:600],"facts":mine(text)}).encode()
        self.send_response(200); self.send_header("Content-Type","application/json"); self.send_header("Content-Length",str(len(out))); self.end_headers(); self.wfile.write(out)
if __name__=="__main__": HTTPServer(("0.0.0.0",int(os.getenv("PORT","9090"))),Handler).serve_forever()
