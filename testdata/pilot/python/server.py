from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import signal
import threading
import time
from urllib.parse import parse_qs, urlparse


ready = threading.Event()
ready.set()
shutdown_requested = threading.Event()


class GracefulServer(ThreadingHTTPServer):
    daemon_threads = False
    block_on_close = True


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        request = urlparse(self.path)
        if request.path == "/ready":
            self.send_response(200 if ready.is_set() else 503)
            self.end_headers()
            return
        if request.path == "/work":
            values = parse_qs(request.query).get("delay_ms", ["2000"])
            try:
                delay_ms = min(max(int(values[0]), 0), 30000)
            except ValueError:
                delay_ms = 2000
            time.sleep(delay_ms / 1000)
            body = b"completed\n"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, message, *args):
        print(f"{self.client_address[0]} - {message % args}", flush=True)


def request_shutdown(signum, _frame):
    print(f"received signal {signum}; draining requests", flush=True)
    ready.clear()
    shutdown_requested.set()


signal.signal(signal.SIGTERM, request_shutdown)
signal.signal(signal.SIGINT, request_shutdown)

server = GracefulServer(("0.0.0.0", 8080), Handler)
server_thread = threading.Thread(target=server.serve_forever, name="http-server", daemon=True)
server_thread.start()
print("Python pilot listening on port 8080", flush=True)

shutdown_requested.wait()
time.sleep(0.1)
server.shutdown()
server.server_close()
server_thread.join()
print("graceful shutdown complete", flush=True)
