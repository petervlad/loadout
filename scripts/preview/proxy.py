# Forwards to the registry, adding a cookie per listening port, so headless
# Chrome can screenshot signed-in pages.
import http.server, os, socketserver, threading, urllib.request, sys

UP = "http://127.0.0.1:" + os.environ.get("LOADOUT_PREVIEW_PORT", "7431")
PORTS = {7441: "loadout_session=alice-session", 7442: "loadout_session=alice-session; loadout_theme=dark",
         7443: "loadout_session=bogdan-session", 7444: "", 7445: "loadout_challenge=abc.YWxpY2VAYWNtZS5leGFtcGxl", 7446: "loadout_theme=dark", 7447: "loadout_session=bogdan-session; loadout_theme=dark",
         7448: "loadout_session=ada-session", 7449: "loadout_session=ada-session; loadout_theme=dark", 7450: "loadout_session=olga-session"}

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *a, **k): return None
opener = urllib.request.build_opener(NoRedirect)

def handler(cookie):
    class H(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            req = urllib.request.Request(UP + self.path)
            if cookie: req.add_header("Cookie", cookie)
            try:
                resp = opener.open(req)
            except urllib.error.HTTPError as e:
                resp = e
            body = resp.read()
            self.send_response(resp.status if hasattr(resp, "status") else resp.code)
            for k, v in resp.headers.items():
                if k.lower() not in ("transfer-encoding", "connection", "content-length"):
                    self.send_header(k, v)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        def log_message(self, *a): pass
    return H

for port, cookie in PORTS.items():
    srv = socketserver.ThreadingTCPServer(("127.0.0.1", port), handler(cookie))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
threading.Event().wait()
