# xbslinkd — OpenWrt Port of XBSlink (Go Rewrite)

A port of [seuffert/XBSlink](https://github.com/seuffert/XBSlink) (C# / .NET, Xbox 360 & PS3 system-link proxy) to OpenWrt: **xbslinkd** is a dependency-free, pure Go (no cgo/libpcap) daemon that is **fully wire-protocol compatible with the original XBSlink client** (original 0.9.6.0) and can network directly with it.

```mermaid
flowchart LR
    subgraph Local["Local LAN"]
        X1["Xbox / PS3"]
    end
    subgraph Router["OpenWrt Router"]
        D["xbslinkd"]
    end
    subgraph Remote["Remote Site"]
        X2["Xbox / PS3"]
        N["Remote node<br/>XBSlink original / xbslinkd"]
    end
    subgraph Cloud["Cloud"]
        C["cloudlist server<br/>(self-hostable)"]
    end

    X1 -- "packet capture<br/>on br-lan" --> D
    D -- "inject" --> X1
    D -- "UDP 31415" --> N
    N -- "UDP 31415" --> D
    N -- "inject" --> X2
    X2 -- "capture" --> N
    D -- "HTTP" --> C
    N -- "HTTP" --> C
    C -. "node discovery / registration" .- D

Features

    Protocol compatibility: byte-for-byte reimplementation of the upstream C# node-to-node UDP protocol ([type][length LE][payload]) and the cloudlist HTTP protocol (JOIN/UPDATE/LEAVE/SENDHELLO, 29s heartbeat, MD5 cloud password, private IPs replaced by the server with the public source IP, SERVERHELLO port-reachability probe).

    Core tunnel: Ethernet-frame-level forwarding. Broadcast frames are flooded to all remote nodes; unicast frames are routed by "destination MAC learned from remote nodes"; loop prevention (injected-MAC dedup window); gateway IP / privileged port filtering identical to the original.

    Packet capture: Linux AF_PACKET + kernel BPF filter (broadcast OR (IP AND UDP)), with user-space filtering that precisely follows the original rules: Xbox signature (source IP 0.0.0.1), UDP ports 3074-3075 / 14000-14001, optional high-port broadcast forwarding, optional MAC allowlist.
    On a br-lan bridge it automatically switches to per-port capture (the bridge master cannot capture port-to-port unicast frames—a pitfall that also affects the libpcap-based original; this port fixes it).

    Cloud node discovery: fully implemented client-side; the official secudb.de cloud is offline, so a self-hostable cloudlist/index.php is included (identical to upstream, MySQL storage).

    OpenWrt integration: UCI config (/etc/config/xbslink), procd service and boot autostart, automatic firewall WAN rule, LuCI status/config pages (luci-app-xbslink).

    Status output: JSON status file (read by LuCI) + 127.0.0.1:31416/status HTTP endpoint.

    Size: static binary ~5-6 MB (all 6 architectures supported), runtime memory ~10-20 MB.

Unported features (differences from the original)
Original feature	Status	Notes
GUI / chat / notification sounds	N/A	Routers run headless; chat messages are still received and written to the log
UPnP port mapping	Not needed	The router is itself the gateway; the init script opens the firewall rule directly
NAT mode (MAC-conflict rewriting for multiple hosts on the same LAN)	Not implemented	Not needed in a single-router scenario; compatibility hooks are left in the protocol layer
Windows raw-socket capture	N/A	Replaced by Linux AF_PACKET
Quick start
Option A: build an ipk with the OpenWrt SDK (recommended, 23.05 / 24.10)
sh

# 1. Download and extract the SDK for your architecture, then enter the SDK directory
./scripts/feeds update base packages luci
./scripts/feeds install -a

# 2. Install this package (copy the whole directory into buildroot)
cp -a xbslink-openwrt/openwrt/xbslinkd      package/
cp -a xbslink-openwrt/openwrt/luci-app-xbslink package/

# 3. Select the package
make menuconfig    # Network -> Routing and Redirection -> xbslinkd
                   # LuCI -> Applications -> luci-app-xbslink

# 4. Build
make package/xbslinkd/compile V=s
make package/luci-app-xbslink/compile V=s
# Output: bin/packages/<arch>/*.ipk

    Requires feeds/packages to provide the golang toolchain; the first build of the Go toolchain is slow.

Option B: cross-compile directly (no SDK needed)
sh

cd xbslink-openwrt
./build.sh mipsle        # or mips / armv7 / arm64 / 386 / amd64
# Output: dist/xbslinkd.linux.mipsle etc.

Manual install on the router (mipsle example):
sh

scp dist/xbslinkd.linux.mipsle root@192.168.1.1:/usr/bin/xbslinkd
scp openwrt/xbslinkd/files/xbslink.config root@192.168.1.1:/etc/config/xbslink
scp openwrt/xbslinkd/files/xbslink.init  root@192.168.1.1:/etc/init.d/xbslink
ssh root@192.168.1.1 "chmod +x /usr/bin/xbslinkd /etc/init.d/xbslink \
  && /etc/init.d/xbslink enable && /etc/init.d/xbslink start"

Option C: install prebuilt ipks directly (no compilation)

openwrt/ipk/ ships prebuilt ipks (regenerable at any time with openwrt/build_ipk.sh):
File	Description
luci-app-xbslink_0.9.7.5-1_all.ipk	Web UI, architecture-independent (depends on xbslinkd)
xbslinkd_0.9.7.5-1_<arch>.ipk	Daemon + procd service + UCI config
luci-i18n-xbslink-<lang>_0.9.7.5-1_all.ipk	Optional UI translations (zh-cn / zh-tw / ja / ru)

<arch> is the opkg architecture name and must match the firmware. Run opkg print-architecture on the router to confirm (common arm64: aarch64_cortex-a53 / aarch64_cortex-a72 / aarch64_generic):
sh

opkg install xbslinkd_0.9.7.5-1_aarch64_cortex-a53.ipk   # install the daemon first
opkg install luci-app-xbslink_0.9.7.5-1_all.ipk          # then the web UI
opkg install luci-i18n-xbslink-zh-cn_0.9.7.5-1_all.ipk   # optional: Simplified Chinese translation

After installing a translation package, select the language in LuCI under "System -> System -> Language and Style" and refresh the page (other languages correspond to -zh-tw / -ja / -ru). The Simplified/Traditional Chinese wording matches the Windows localized version (room name, direct connect to remote host, listen port, "always forward" MAC list, etc.). Translations are installed as .lmo files under /usr/lib/lua/luci/i18n/xbslink.<lang>.lmo. Source files are in po/<lang>/xbslink.po; recompiling requires LuCI's po2lmo tool (openwrt/luci repo, modules/luci-base/src):
sh

PO2LMO=/path/to/po2lmo ./build_ipk.sh    # automatically recompiles .lmo during build

After installation, go to LuCI "Network -> XBSlink" to configure: check Enable, select the capture interface (br-lan), fill in peer addresses, save, and restart the service. Note: the menu depends on /etc/config/xbslink, so the xbslinkd package must be installed first.

    Troubleshooting: if opkg install reports pkg_init_from_file: Malformed package file, either the file transfer was corrupted (verify with md5sum against your local copy), or opkg is too old to recognize the Debian ar format (the ipks in this repo already default to the legacy tar.gz format compatible with all opkg versions; IPK_FORMAT=ar ./build_ipk.sh switches back to ar format).

Basic configuration
sh

uci set xbslink.config.enabled='1'
uci set xbslink.config.interface='br-lan'     # kernel device name, check with ip link show
uci set xbslink.config.nickname='Living Room Router'
# Direct connection to a friend's fixed node (multiple allowed, list syntax):
uci add_list xbslink.config.peers='1.2.3.4:31415'
uci commit xbslink
/etc/init.d/xbslink restart

Check status:
sh

cat /var/run/xbslinkd/status.json | jsonfilter -e @.nodes -i -   # or
curl 127.0.0.1:31416/status                                      # or
logread | grep xbslink

After installing luci-app-xbslink, view the live node table/latency/traffic and manage configuration on the Network → XBSlink page.
Cloud play (automatic node discovery)

The original official cloud is offline. Since 0.9.7.5, the default cloud server points to a private cloud-list service at http://10.165.0.105/cloudlist.php, which can be changed in LuCI or UCI. Two options:

    Self-host a cloud (recommended): deploy cloudlist/index.php on any PHP+MySQL host (original protocol, unmodified), then:
    sh

    uci set xbslink.config.cloud_enabled='1'
    uci set xbslink.config.cloud_server='http://your-server/cloudlist.php'
    uci set xbslink.config.cloud_name='My Room'

    When enabled, the init script automatically opens udp/31415 in the WAN zone (the cloud server probes whether that port is reachable; once the probe succeeds the node is registered as reachable and public nodes can connect directly).

    Static nodes only: don't enable the cloud, just use the peers list—suitable for small rooms of two or three people.

How it works & protocol

The complete wire-protocol specification (message formats, state machine, cloud API, capture/injection filtering rules) is in docs/PROTOCOL.md—reverse-engineered from the upstream C# source and the basis for this port's compatibility.
Testing

    Unit tests (protocol encode/decode): go test ./...

    Interop regression against the original client (simulating the Windows XBSlink GUI's 1-byte datagram handshake):
    sh

    go build -o /tmp/xbslinkd . && cd tests && unshare -Urn bash test_interop.sh

    Integration test (two instances + two "hosts" broadcast/unicast loopback + loop prevention, run in a user namespace):
    sh

    go build -o /tmp/xbslinkd . && cd tests && unshare -Urn bash test_loop.sh

    Cloud client test (built-in fake cloudlist server, covering the full SENDHELLO/JOIN/UPDATE/LEAVE flow):
    sh

    unshare -Urn bash tests/test_cloud.sh

Current status: interop tests 3/3, tunnel tests 7/7, cloud tests 5/5 passing.
The interop tests cover a key wire behavior of the original GUI: when not joined to a cloud, ANNOUNCE / GETNODELIST / GETCLIENTVERSION / GETNICKNAME are all sent as 1-byte datagrams (getByteArray() omits the length header when the payload is null), and the receiver accepts datagrams of ≤3 bytes as zero-payload—strict length validation would make handshaking with the original impossible.
Known limitations and caveats

    Cross-subnet receivers: tunneled Ethernet frames retain their original source IP. If the remote receiver is Linux with rp_filter (strict reverse-path validation) enabled, broadcasts from other subnets may be dropped. Xbox/PS3 consoles don't perform this check; only the router itself as receiver is affected. Real Xbox traffic has a source IP of 0.0.0.1, which is inherently unaffected.

    Performance: 100 Mbps-class play traffic (UDP 3074) is no burden on MT7621/MT7981-class CPUs; the core path is a single memory copy plus a UDP send, with no encryption overhead.

    IPv6: The XBSlink protocol itself is IPv4-only; this port is consistent with that.

    Cloud passwords are transmitted as plaintext MD5 (as in the upstream protocol) and are only used for cloud create/join validation.

Directory structure
text

xbslink-openwrt/
├── main.go ...          Go sources (single package, module github.com/xbslink/xbslinkd, dependencies vendored)
├── message_test.go      Unit tests for protocol encode/decode
├── build.sh             Cross-compile script without SDK (6 architectures)
├── docs/
│   ├── PROTOCOL.md      Wire-protocol specification (reverse-engineered from the C# source)
│   └── BUILD.md         Detailed build and installation guide
├── openwrt/
│   ├── xbslinkd/        OpenWrt package (Makefile + UCI + procd init + src/ copy)
│   └── luci-app-xbslink/  LuCI web UI (JS views + menu + ACL)
├── cloudlist/index.php  Self-hostable cloud node-list server (upstream original)
└── tests/               Integration tests (test_loop.sh / test_cloud.sh / host simulators)

License

The original XBSlink is GPL-2.0-or-later; this port is a derivative independent implementation and uses the same license (see COPYING).
cloudlist/index.php is taken from the upstream repository.