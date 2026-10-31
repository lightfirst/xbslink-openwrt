#!/usr/bin/env python3
"""UDP listener - simulates a game console receiving system link broadcasts.

Prints every received datagram as JSON lines; exits on timeout.
"""
import json
import socket
import sys
import time

port = int(sys.argv[1])
timeout = float(sys.argv[2])
expect_tag = sys.argv[3] if len(sys.argv) > 3 else "XBS-TEST"

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
try:
    s.bind(("0.0.0.0", port))
except OSError as e:
    print(json.dumps({"error": str(e)}), flush=True)
    sys.exit(1)
s.settimeout(1.0)

deadline = time.time() + timeout
got = []
while time.time() < deadline:
    try:
        data, addr = s.recvfrom(4096)
    except socket.timeout:
        continue
    msg = data.decode(errors="replace")
    got.append(msg)
    print(json.dumps({"from": addr[0], "payload": msg, "tag_ok": expect_tag in msg}), flush=True)

print(json.dumps({"total": len(got), "tag_ok": sum(1 for g in got if expect_tag in g)}), flush=True)
sys.exit(0 if got else 2)
