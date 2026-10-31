#!/usr/bin/env python3
"""Fake XBSlink cloudlist server for testing the cloud client.

Implements GETLIST / JOIN / UPDATE / LEAVE / SENDHELLO exactly like
cloudlist/index.php (including UDP PING reachability probe and SERVERHELLO).
"""
import socket
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse, parse_qs

LOG = open("/tmp/fakecloud.log", "w")


def log(*a):
    print(*a, file=LOG, flush=True)


class Cloud:
    def __init__(self):
        self.nodes = {}  # uuid -> {ip, port, nick}
        self.next_uuid = 1700000000

    def join(self, q, client_ip):
        node_ip = q.get("node_ip", [""])[0]
        node_port = int(q.get("node_port", ["0"])[0])
        nick = q.get("nick", [""])[0]
        if node_ip in ("0.0.0.0", "") or node_ip.startswith(("192.168.", "10.", "172.")):
            node_ip = client_ip
        # reachability probe: UDP ping, expect any reply in 1s
        reachable = self.udp_ping(node_ip, node_port)
        for uuid, n in self.nodes.items():
            if n["ip"] == node_ip and n["port"] == node_port:
                return "OK:%s\n" % uuid
        uuid = str(self.next_uuid)
        self.next_uuid += 1
        self.nodes[uuid] = {"ip": node_ip, "port": node_port, "nick": nick}
        log("JOIN %s:%d nick=%s reachable=%s" % (node_ip, node_port, nick, reachable))
        rows = "".join(
            "\nnode_ip=%s&node_port=%d" % (n["ip"], n["port"])
            for u, n in self.nodes.items() if u != uuid
        )
        # plus a phantom node to test discovery from UPDATE
        return "OK:%s%s\n" % (uuid, rows)

    def update(self, q, client_ip):
        uuid = q.get("uuid", [""])[0]
        if uuid not in self.nodes:
            return "ERROR:unknown uuid\n"
        self.nodes[uuid]["last"] = time.time()
        rows = "".join(
            "\nnode_ip=%s&node_port=%d" % (n["ip"], n["port"])
            for u, n in self.nodes.items() if u != uuid
        )
        # simulate a second node joining the cloud since JOIN
        if "phantom" not in self.nodes:
            self.nodes["phantom"] = {"ip": "203.0.113.77", "port": 31415, "nick": "phantom"}
            log("phantom node injected")
        rows = "".join(
            "\nnode_ip=%s&node_port=%d" % (n["ip"], n["port"])
            for u, n in self.nodes.items() if u != uuid
        )
        return "OK:%s%s\n" % (uuid, rows)

    def udp_ping(self, ip, port):
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.settimeout(1.0)
        try:
            s.sendto(b"\x04\x08\x00\x00\x00\x00\x00\x00\x00\x00\x00", (ip, port))
            s.recvfrom(100)
            return True
        except Exception:
            return False
        finally:
            s.close()


cloud = Cloud()


class H(BaseHTTPRequestHandler):
    def do_GET(self):
        u = urlparse(self.path)
        q = parse_qs(u.query)
        cmd = q.get("cmd", [""])[0]
        client_ip = self.client_address[0]
        log("REQ from %s: %s" % (client_ip, self.path))
        if cmd == "GETLIST":
            body = "OK:\ncloudname=testcloud&countnodes=1&maxnodes=10&password=False\n"
        elif cmd == "JOIN":
            body = cloud.join(q, client_ip)
        elif cmd == "UPDATE":
            body = cloud.update(q, client_ip)
        elif cmd == "LEAVE":
            body = "OK:\n"
        elif cmd == "SENDHELLO":
            ip = q.get("node_ip", [""])[0]
            port = int(q.get("node_port", ["0"])[0])
            if ip.startswith(("192.168.", "10.", "172.")) or ip == "0.0.0.0":
                ip = client_ip
            s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            s.sendto(b"\xff\x00\x00", (ip, port))
            s.close()
            body = "OK:\n"
        else:
            body = "ERROR:no command\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.end_headers()
        self.wfile.write(body.encode())

    def log_message(self, *a):
        pass


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8099
    HTTPServer(("127.0.0.1", port), H).serve_forever()
