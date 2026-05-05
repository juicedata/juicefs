#!/usr/bin/env python3
"""
Mock traffic control server for testing juicefs sync --traffic-control-url.

Protocol:
  POST / with JSON body: {"bytes": N}
    - Positive N: request N bytes of bandwidth quota
    - Negative N: payback -N bytes of unused quota
  Response JSON: {"granted": N, "expired": M}
    - granted: bytes granted
    - expired: milliseconds until the grant expires

Usage:
  python3 traffic_control_server.py [--port PORT] [--bwlimit BYTES_PER_SEC] [--log LOG_FILE]

Options:
  --port PORT            Port to listen on (default: 12345)
  --bwlimit BYTES_PER_SEC  Global bandwidth limit in bytes/sec (default: 0 = unlimited)
  --log LOG_FILE         Log file for request/response details (default: /tmp/tc_server.log)
"""

import argparse
import json
import threading
import time
from http.server import HTTPServer, BaseHTTPRequestHandler


class TrafficState:
    def __init__(self, bwlimit=0):
        self.lock = threading.Lock()
        self.bwlimit = bwlimit  # bytes per second, 0 = unlimited
        self.total_granted = 0
        self.total_payback = 0
        self.request_count = 0
        self.last_grant_time = time.time()
        self.balance = 0  # available bytes in current window
        self.log_file = None

    def request(self, asked_bytes):
        with self.lock:
            self.request_count += 1
            if asked_bytes < 0:
                # Payback
                self.total_payback += abs(asked_bytes)
                self.balance += abs(asked_bytes)
                return 0, 1000

            if self.bwlimit <= 0:
                # Unlimited: grant everything, 5 second expiry
                self.total_granted += asked_bytes
                return asked_bytes, 5000

            # Rate-limited mode: grant up to bwlimit bytes per second
            now = time.time()
            elapsed = now - self.last_grant_time
            self.balance += self.bwlimit * elapsed
            if self.balance > self.bwlimit * 2:
                self.balance = self.bwlimit * 2
            self.last_grant_time = now

            granted = min(asked_bytes, max(int(self.balance), 1024))
            self.balance -= granted
            self.total_granted += granted
            # Short expiry when rate limiting to force re-requests
            return granted, 1000


state = TrafficState()


class TCHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        content_length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(content_length)
        try:
            req = json.loads(body)
            asked = req.get("bytes", 0)
        except (json.JSONDecodeError, KeyError):
            self.send_error(400, "Invalid JSON")
            return

        granted, expired = state.request(asked)
        resp = {"granted": granted, "expired": expired}
        resp_body = json.dumps(resp).encode()

        if state.log_file:
            with open(state.log_file, "a") as f:
                f.write(f"req={asked} granted={granted} expired={expired} "
                        f"total_granted={state.total_granted} total_payback={state.total_payback} "
                        f"requests={state.request_count}\n")

        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(resp_body)))
        self.end_headers()
        self.wfile.write(resp_body)

    def log_message(self, format, *args):
        # Suppress default HTTP logging to keep test output clean
        pass


def main():
    parser = argparse.ArgumentParser(description="Mock traffic control server")
    parser.add_argument("--port", type=int, default=12345, help="Port to listen on")
    parser.add_argument("--bwlimit", type=int, default=0, help="Bandwidth limit in bytes/sec (0=unlimited)")
    parser.add_argument("--log", type=str, default="/tmp/tc_server.log", help="Log file path")
    args = parser.parse_args()

    global state
    state = TrafficState(bwlimit=args.bwlimit)
    state.log_file = args.log

    # Clear log file
    with open(args.log, "w") as f:
        f.write("")

    server = HTTPServer(("0.0.0.0", args.port), TCHandler)
    print(f"Traffic control server listening on 0.0.0.0:{args.port} (bwlimit={args.bwlimit})")
    server.serve_forever()


if __name__ == "__main__":
    main()
