import http.client
import importlib.util
import json
import threading
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("app.py")
SPEC = importlib.util.spec_from_file_location("openbid_extractor_app", MODULE_PATH)
extractor_app = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(extractor_app)


class ExtractorHelperTests(unittest.TestCase):
    def test_parse_html_strips_scripts_styles_and_tags(self):
        text = extractor_app.parse_html(
            b"<html><head><style>.x{color:red}</style><script>alert(1)</script></head>"
            b"<body><h1>Tender Notice</h1><p>Visible copy</p></body></html>"
        )

        self.assertEqual(text, "Tender Notice Visible copy")

    def test_normalize_datetime_handles_dates_and_12_hour_time(self):
        self.assertEqual(extractor_app.normalize_date("4 April 2026"), "2026-04-04")
        self.assertEqual(extractor_app.normalize_time("2:30 pm"), "14:30")
        self.assertEqual(
            extractor_app.normalize_datetime("4 April 2026", "2:30 pm"),
            "2026-04-04 14:30",
        )

    def test_mine_extracts_key_tender_fields(self):
        text = """
        RFB Number: ENG-42
        Date of issuance: 4 April 2026
        CLOSING DATE: 12 April 2026 CLOSING TIME: 11:00
        Evaluation method: 80/20
        Minimum functionality score 70%
        CIDB grading 6 GB
        Contact: Jane Doe
        jane@example.com
        012 345 6789
        """

        facts = extractor_app.mine(text)

        self.assertEqual(facts["issued_date"], "2026-04-04")
        self.assertEqual(facts["closing_date"], "2026-04-12")
        self.assertEqual(facts["closing_time"], "11:00")
        self.assertEqual(facts["closing_datetime"], "2026-04-12 11:00")
        self.assertEqual(facts["evaluation_method"], "80/20")
        self.assertEqual(facts["price_points"], "80")
        self.assertEqual(facts["preference_points"], "20")
        self.assertEqual(facts["minimum_functionality_score"], "70")
        self.assertEqual(facts["cidb_grade"], "6GB")
        self.assertEqual(facts["contact_email"], "jane@example.com")
        self.assertEqual(facts["contact_phone"], "012 345 6789")

    def test_validate_public_url_rejects_private_resolution(self):
        with mock.patch.object(
            extractor_app.socket,
            "getaddrinfo",
            return_value=[(0, 0, 0, "", ("127.0.0.1", 0))],
        ):
            with self.assertRaisesRegex(ValueError, "private or local network urls are not allowed"):
                extractor_app.validate_public_url("https://example.invalid/doc.pdf")


class ExtractorHTTPTests(unittest.TestCase):
    def setUp(self):
        self.server = extractor_app.HTTPServer(("127.0.0.1", 0), extractor_app.Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.port = self.server.server_address[1]
        self.log_patch = mock.patch.object(
            extractor_app.Handler,
            "log_message",
            autospec=True,
            side_effect=lambda *_args, **_kwargs: None,
        )
        self.log_patch.start()

    def tearDown(self):
        self.log_patch.stop()
        self.server.shutdown()
        self.thread.join(timeout=5)
        self.server.server_close()

    def request(self, method, path, payload=None):
        conn = http.client.HTTPConnection("127.0.0.1", self.port, timeout=5)
        body = b""
        headers = {}
        if payload is not None:
            body = json.dumps(payload).encode("utf-8")
            headers["Content-Type"] = "application/json"
            headers["Content-Length"] = str(len(body))
        conn.request(method, path, body=body, headers=headers)
        resp = conn.getresponse()
        data = resp.read()
        conn.close()
        return resp.status, data

    def test_healthz_reports_ok(self):
        status, body = self.request("GET", "/healthz")

        self.assertEqual(status, 200)
        self.assertEqual(json.loads(body), {"ok": True})

    def test_extract_returns_pdf_payload(self):
        with mock.patch.object(extractor_app, "fetch", return_value=b"%PDF-1.4"), mock.patch.object(
            extractor_app,
            "parse_pdf",
            return_value="Tender Title\nCLOSING DATE: 12 April 2026 CLOSING TIME: 10:00",
        ):
            status, body = self.request("POST", "/extract", {"url": "https://example.com/tender.pdf"})

        payload = json.loads(body)
        self.assertEqual(status, 200)
        self.assertEqual(payload["type"], "pdf")
        self.assertEqual(payload["facts"]["closing_date"], "2026-04-12")
        self.assertEqual(payload["facts"]["closing_time"], "10:00")

    def test_extract_returns_html_payload(self):
        with mock.patch.object(
            extractor_app,
            "fetch",
            return_value=(
                b"<html><body><h1>Water upgrade tender</h1>"
                b"<script>ignore()</script><p>Closing Date: 5 April 2026</p></body></html>"
            ),
        ):
            status, body = self.request("POST", "/extract", {"url": "https://example.com/tender.html"})

        payload = json.loads(body)
        self.assertEqual(status, 200)
        self.assertEqual(payload["type"], "html")
        self.assertIn("Water upgrade tender", payload["excerpt"])
        self.assertNotIn("ignore()", payload["excerpt"])
        self.assertEqual(payload["facts"]["closing_date"], "2026-04-05")

    def test_extract_returns_error_payload_when_fetch_fails(self):
        with mock.patch.object(extractor_app, "fetch", side_effect=ValueError("blocked")):
            status, body = self.request("POST", "/extract", {"url": "https://example.com/bad.pdf"})

        self.assertEqual(status, 400)
        self.assertEqual(json.loads(body), {"error": "blocked"})


if __name__ == "__main__":
    unittest.main()
