#!/usr/bin/env python3
"""
Lore Demo Proxy — bridges the frontend to the Go integration test.
Runs the Go test and streams its output as SSE to the browser.
"""
import http.server, subprocess, threading, queue, json, os, sys, time

PORT = 8080
TUTORIAL_DIR = os.path.expanduser("~/canopy-lore/plugin/go/tutorial")

class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/run":
            self.run_test()
        else:
            super().do_GET()

    def do_OPTIONS(self):
        self.send_response(200)
        self._cors()
        self.end_headers()

    def _cors(self):
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Methods", "GET, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "*")

    def run_test(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self._cors()
        self.end_headers()

        def send(event, data):
            try:
                msg = f"event: {event}\ndata: {json.dumps(data)}\n\n"
                self.wfile.write(msg.encode())
                self.wfile.flush()
            except:
                pass

        send("start", {"msg": "Starting Lore integration test..."})

        try:
            proc = subprocess.Popen(
                [
                    "go", "test", "-v",
                    "-run", "TestLoreProtocol",
                    "-timeout", "480s"
                ],
                cwd=TUTORIAL_DIR,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                text=True,
                env={**os.environ, "GOTOOLCHAIN": "local"}
            )

            for line in iter(proc.stdout.readline, ""):
                line = line.rstrip()
                if not line:
                    continue
                # classify the line
                if "PASS" in line or "confirmed" in line.lower() or "funded" in line.lower() or "✓" in line:
                    level = "success"
                elif "FAIL" in line or "Error" in line or "error" in line:
                    level = "error"
                elif "Step" in line or "Waiting" in line or "Submitting" in line:
                    level = "info"
                else:
                    level = "log"

                # extract step signals
                step = None
                if "Step 2" in line or "Funding" in line:
                    step = "fund"
                elif "Step 3" in line or "publish_thread" in line:
                    step = "publish"
                elif "Step 4" in line or "set_supporter_threshold" in line:
                    step = "threshold"
                elif "Step 5" in line or "tip_creator" in line:
                    step = "tip"
                elif "Step 6" in line or "claim_tips" in line:
                    step = "claim"

                send("line", {"msg": line, "level": level, "step": step})

            proc.wait()
            if proc.returncode == 0:
                send("done", {"success": True,  "msg": "All Lore transactions confirmed!"})
            else:
                send("done", {"success": False, "msg": "Test failed — check logs above"})

        except Exception as e:
            send("done", {"success": False, "msg": str(e)})

    def log_message(self, *args):
        pass  # silence request logs

os.chdir(os.path.dirname(os.path.abspath(__file__)))
print(f"Lore demo server running at http://localhost:{PORT}")
print(f"Open http://localhost:{PORT} in your browser")
http.server.HTTPServer(("", PORT), Handler).serve_forever()
