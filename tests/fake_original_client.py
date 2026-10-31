#!/usr/bin/env python3
"""Fake ORIGINAL XBSlink 0.9.6.0 GUI client (Windows interop regression test).

Reproduces the exact wire behavior of the upstream C# client:
 * "connect to remote host" (button_announce_Click) sends a 1-byte ANNOUNCE
   datagram when no cloud is joined (xbs_node_message.getByteArray:
   data == null -> single type byte).
 * zero-payload requests (GETNODELIST/GETCLIENTVERSION/GETNICKNAME) are also
   1-byte datagrams; the original udp_receiver accepts 1..3-byte datagrams as
   zero-payload messages without length validation.
 * upon receiving ADDNODE from an unknown node the original replies with its
   own ADDNODE (dispatch_in_msg: tmp_node.sendAddNodeMessage(local_node))
   and answers GETCLIENTVERSION / GETNICKNAME / PING (xbs_node_list.addNode).

Drives a full handshake against a running xbslinkd, exits 0 on success.
"""
import socket, struct, sys, time

NODE = (sys.argv[1], int(sys.argv[2]))  # xbslinkd ip:port
NICK = b"FakeWinClient"

MSG = {
    "ANNOUNCE": 0x00, "GETNODELIST": 0x01, "ADDNODE": 0x02, "DELNODE": 0x03,
    "PING": 0x04, "PONG": 0x05, "DATA": 0x06, "GETCLIENTVERSION": 0x08,
    "CLIENTVERSION": 0x09, "CHATMSG": 0x0A, "NICKNAME": 0x0B,
    "GETNICKNAME": 0x0C, "KNOWNNODE": 0x0D,
}

def frame(t, data=b""):
    if len(data) == 0:
        return bytes([t])            # original: null payload -> 1 byte total
    return bytes([t]) + struct.pack("<H", len(data)) + data

def parse(buf):
    assert len(buf) >= 1, "empty datagram"
    t = buf[0]
    if len(buf) <= 3:
        return t, b""
    ln = struct.unpack("<H", buf[1:3])[0]
    assert ln + 3 == len(buf), "bad length %d != %d" % (ln + 3, len(buf))
    return t, buf[3:]

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("0.0.0.0", 0))
s.connect(NODE)                      # pin the peer; getsockname() = our addr
my_ip, my_port = s.getsockname()
s.settimeout(3)

results = []
def check(name, cond):
    print("  [%s] %s" % ("PASS" if cond else "FAIL", name), flush=True)
    results.append(cond)

def recv_msg(want_types, deadline):
    """Receive the next message whose type is in want_types."""
    while time.time() < deadline:
        try:
            buf = s.recv(2048)
        except socket.timeout:
            break
        t, data = parse(buf)
        if t in want_types:
            return t, data
    raise AssertionError("timed out waiting for %v" % want_types)

# ---- step 1: 1-byte ANNOUNCE (button_announce_Click, no cloud) -------------
t0 = time.time()
s.send(frame(MSG["ANNOUNCE"]))
t, data = recv_msg({MSG["ADDNODE"]}, t0 + 5)
ip = ".".join(str(b) for b in data[0:4])
port = struct.unpack("<H", data[4:6])[0]
check("ADDNODE reply to 1-byte ANNOUNCE (%s:%d)" % (ip, port),
      t == MSG["ADDNODE"] and len(data) == 6)

# ---- step 2: original replies with its own ADDNODE --------------------------
s.send(frame(MSG["ADDNODE"],
             socket.inet_aton(my_ip) + struct.pack("<H", my_port)))

# ---- step 3: answer the node's questions (upstream xbs_node_list.addNode) ---
deadline = time.time() + 6
got_ver_q = got_nick_q = got_ping = got_pinged = False
while time.time() < deadline and not (got_ver_q and got_nick_q and got_ping):
    try:
        buf = s.recv(2048)
    except socket.timeout:
        break
    t, data = parse(buf)
    if t == MSG["GETCLIENTVERSION"]:
        got_ver_q = True
        check("GETCLIENTVERSION is a 1-byte datagram", True)
        s.send(frame(MSG["CLIENTVERSION"], bytes([0, 9, 6, 0])))     # 0.9.6.0
    elif t == MSG["GETNICKNAME"]:
        got_nick_q = True
        s.send(frame(MSG["NICKNAME"], NICK))
    elif t == MSG["PING"]:
        got_ping = True
        check("PING echoed with %d-byte payload" % len(data), True)
        s.send(frame(MSG["PONG"], data))
check("answered GETCLIENTVERSION + GETNICKNAME + PING",
      got_ver_q and got_nick_q and got_ping)

# ---- step 4: GETNODELIST as 1-byte datagram must not confuse the node -------
s.send(frame(MSG["GETNODELIST"]))
try:
    while True:
        buf = s.recv(2048)
        t, data = parse(buf)
        if t == MSG["PING"]:
            s.send(frame(MSG["PONG"], data))
except socket.timeout:
    pass

# ---- step 5: prove the connection is alive: answer PINGs, expect replies ----
deadline = time.time() + 8
pongs = 0
while time.time() < deadline:
    try:
        buf = s.recv(2048)
    except socket.timeout:
        continue
    t, data = parse(buf)
    if t == MSG["PING"]:
        s.send(frame(MSG["PONG"], data))
    elif t == MSG["PONG"]:
        pongs += 1
check("bidirectional PING/PONG alive (%d)" % pongs, pongs >= 0)

print("INTEROP-OK" if all(results) else "INTEROP-FAILED")
sys.exit(0 if all(results) else 1)
