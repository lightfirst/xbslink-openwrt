#!/usr/bin/env python3
"""Raw Ethernet frame sender - simulates game console network behavior.

Runs as root inside a user+network namespace. Crafts IPv4/UDP frames the way
consoles do for system link traffic (L2 broadcast / unicast) and writes them
onto an interface via AF_PACKET.
"""
import socket
import struct
import sys


def csum(data):
    if len(data) % 2:
        data += b"\x00"
    s = 0
    for i in range(0, len(data), 2):
        s += (data[i] << 8) | data[i + 1]
    while s >> 16:
        s = (s & 0xFFFF) + (s >> 16)
    return (~s) & 0xFFFF


def build_udp_frame(src_mac, dst_mac, src_ip, dst_ip, sport, dport, payload):
    udp_len = 8 + len(payload)
    udp = struct.pack("!HHHH", sport, dport, udp_len, 0) + payload
    pseudo = (
        socket.inet_aton(src_ip)
        + socket.inet_aton(dst_ip)
        + struct.pack("!BBH", 0, 17, udp_len)
    )
    c = csum(pseudo + udp)
    if c == 0:
        c = 0xFFFF
    udp = udp[:6] + struct.pack("!H", c) + udp[8:]

    total_len = 20 + udp_len
    ip = struct.pack(
        "!BBHHHBBH4s4s",
        0x45,
        0,
        total_len,
        0,
        0,
        64,
        17,
        0,
        socket.inet_aton(src_ip),
        socket.inet_aton(dst_ip),
    )
    ip = ip[:10] + struct.pack("!H", csum(ip)) + ip[12:]

    def m(x):
        return bytes.fromhex(x.replace(":", ""))

    return m(dst_mac) + m(src_mac) + b"\x08\x00" + ip + udp


def main():
    iface, src_mac, src_ip, mode, count = (
        sys.argv[1],
        sys.argv[2],
        sys.argv[3],
        sys.argv[4],
        int(sys.argv[5]),
    )
    if mode == "bcast":
        # Xbox-style system link broadcast: L2 broadcast, IP dst 255.255.255.255
        dst_mac, dst_ip, dport = "ff:ff:ff:ff:ff:ff", "255.255.255.255", 3074
    elif mode == "ucast":
        dst_mac, dst_ip, dport = sys.argv[6], sys.argv[7], 3074
    else:
        raise SystemExit("mode must be bcast|ucast")

    s = socket.socket(socket.AF_PACKET, socket.SOCK_RAW)
    s.bind((iface, 0))
    for i in range(count):
        payload = ("XBS-TEST-%s-%d" % (mode, i)).encode()
        frame = build_udp_frame(src_mac, dst_mac, src_ip, dst_ip, 3074, dport, payload)
        sent = s.send(frame)
        assert sent == len(frame)
    print("sent %d frames" % count, flush=True)


if __name__ == "__main__":
    main()
