#!/usr/bin/env python3
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

SURVEYS = {
    111: {"completed": 5, "incomplete": 1, "full": 4, "active": "Y", "title": "Survey 111"},
    222: {"completed": 2, "incomplete": 0, "full": 2, "active": "Y", "title": "Survey 222"},
    333: {"completed": 9, "incomplete": 2, "full": 8, "active": "N", "title": "Survey 333"},
}


def rpc_result(method, params):
    if method == "get_session_key":
        return "mock-session"
    if method == "release_session_key":
        return "OK"
    if method == "list_surveys":
        return [
            {"sid": sid, "surveyls_title": item["title"], "active": item["active"]}
            for sid, item in sorted(SURVEYS.items())
        ]
    if method == "get_summary":
        survey_id = int(params[1])
        item = SURVEYS[survey_id]
        return {
            "CompletedResponses": item["completed"],
            "IncompleteResponses": item["incomplete"],
            "FullResponses": item["full"],
        }
    if method == "get_survey_properties":
        survey_id = int(params[1])
        item = SURVEYS[survey_id]
        return {"active": item["active"]}
    raise KeyError(method)


class Handler(BaseHTTPRequestHandler):
    def _send(self, code, body, content_type="application/json; charset=utf-8"):
        payload = body.encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_POST(self):
        if self.path != "/jsonrpc":
            self._send(404, json.dumps({"error": "not found"}))
            return
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        request = json.loads(raw.decode("utf-8"))
        try:
            result = rpc_result(request.get("method"), request.get("params", []))
            response = {"jsonrpc": "2.0", "id": request.get("id", 1), "result": result}
        except Exception as exc:
            response = {
                "jsonrpc": "2.0",
                "id": request.get("id", 1),
                "error": {"code": -32000, "message": str(exc)},
            }
        self._send(200, json.dumps(response))

    def do_GET(self):
        parsed = urlparse(self.path)
        if not parsed.path.startswith("/surveys/"):
            self._send(404, "not found", "text/plain; charset=utf-8")
            return
        try:
            survey_id = int(parsed.path.rsplit("/", 1)[-1])
        except ValueError:
            self._send(400, "bad survey id", "text/plain; charset=utf-8")
            return
        params = parse_qs(parsed.query)
        body = f"""<!doctype html>
<html>
  <body>
    <h1>Mock Survey {survey_id}</h1>
    <div id=\"survey-id\">{survey_id}</div>
    <pre id=\"query\">{json.dumps(params, sort_keys=True)}</pre>
  </body>
</html>"""
        self._send(200, body, "text/html; charset=utf-8")

    def log_message(self, fmt, *args):
        print(fmt % args)


if __name__ == "__main__":
    HTTPServer(("0.0.0.0", 19080), Handler).serve_forever()
