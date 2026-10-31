#!/usr/bin/env python3
"""Raw AF_PACKET frame recorder: prints HIT + one hex summary line per frame
whose Ethernet dst MAC is broadcast or matches a pattern. Used by tests."""
import socket, sys, struct, time

ifname = sys.argv[1]
seconds = float(sys.argv[2]) if len(sys.argv) > 2 else 10

s = socket.socket(socket.AF_PACKET, socket.SOCK_RAW, socket.ntohs(0x0003))
s.bind((ifname, 0))
s.settimeout(0.5)
deadline = time.time() + seconds
hits = 0
while time.time() < deadline:
    try:
        frame, _ = s.recvfrom(65535)
    except socket.timeout:
        continue
    if len(frame) < 14:
        continue
    hits += 1
    print("HIT dst=%s src=%s len=%d" % (
        frame[0:6].hex(":"), frame[6:12].hex(":"), len(frame)), flush=True)
print("TOTAL %d" % hits, flush=True)
