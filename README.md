# xbslinkd — OpenWrt port of XBSlink (Go rewrite)

This is a port of [seuffert/XBSlink](https://github.com/seuffert/XBSlink)
(C# / .NET, a system-link proxy for Xbox 360 & PS3) to OpenWrt: **xbslinkd** is a
dependency-free, pure-Go (no cgo/libpcap) daemon whose wire protocol is **fully
compatible** with the original XBSlink client (0.9.6.0) — it can network and play
with it directly.

```
┌─────────────┐   capture on br-lan  ┌──────────────┐  UDP 31415   ┌───────────────┐  inject   ┌─────────────┐
│ Xbox / PS3  │ ───────────────────► │  OpenWrt     │ ───────────► │ Remote nodes  │ ────────► │ Xbox / PS3  │
│ (local LAN) │ ◄─────────────────── │  xbslinkd    │ ◄─────────── │ stock XBSlink │ ◄──────── │ (remote LAN)│
└─────────────┘                      │  (on router) │  or peers    │  / xbslinkd   │           └─────────────┘
                                     └──────┬───────┘              └───────┬───────┘
                                            │ HTTP                         │
                                     ┌──────┴──────────┐                   │
                                     │    cloudlist    │◄──────────────────┘
                                     │ (self-hostable) │   node discovery / registration
                                     └─────────────────┘
```

## Features

* **Protocol compatibility**: the node-to-node UDP protocol (`[type][length LE][payload]`)
  and the cloudlist HTTP protocol (JOIN/UPDATE/LEAVE/SENDHELLO, 29 s heartbeat, MD5 cloud
  password, server-side replacement of private IPs with the public source IP, SERVERHELLO
  port-reachability probe) were reproduced byte-for-byte from the upstream C# source.
* **Core tunneling**: Ethernet-frame-level forwarding. Broadcast frames are flooded to all
  remote nodes; unicast frames are routed by destination MACs learned from remote peers;
  loop prevention (injection-MAC deduplication window); gateway-IP / privileged-port
  filtering identical to the original.
* **Capture**: Linux `AF_PACKET` + kernel BPF filter (`broadcast OR (IP and UDP)`), with
  user-space filtering that exactly follows the original rules: Xbox signature (source IP
  `0.0.0.1`), UDP ports 3074-3075 / 14000-14001, optional high-port broadcast forwarding,
  optional special-MAC whitelist.
  **On a br-lan bridge, capture automatically switches to per-port sockets** (the bridge
  master cannot see port-to-port unicast frames — the same pitfall the original
  libpcap-based version had; this port solves it).
* **Cloud node discovery**: fully implemented client. The official `secudb.de` cloud is
  offline; a self-hostable `cloudlist/index.php` (identical to upstream, MySQL storage) is
  included.
* **OpenWrt integration**: UCI configuration (`/etc/config/xbslink`), procd service with
  boot autostart, automatic firewall WAN rules, LuCI status/configuration page
  (`luci-app-xbslink`).
* **Status output**: JSON status file (read by LuCI) + `127.0.0.1:31416/status` HTTP endpoint.
* **Size**: static binary ~5-6 MB (all 6 architectures supported), ~10-20 MB runtime memory.

## Not ported (differences from the original)

| Original feature | Status | Notes |
|---|---|---|
| GUI / chat / sound effects | N/A | Headless on a router; chat messages are still received and logged |
| UPnP port mapping | Not needed | The router itself is the gateway; the init script opens firewall rules directly |
| NAT mode (MAC rewriting for multiple same-segment hosts) | Not implemented | Unneeded for single-router setups; compatibility hooks kept at the protocol level |
| Windows raw-socket capture | N/A | Replaced by Linux AF_PACKET |

## Quick start

### Option A: build the ipk with the OpenWrt SDK (recommended, 23.05 / 24.10)

```sh
# 1. Download & unpack the SDK for your architecture, then cd into the SDK directory
./scripts/feeds update base packages luci
./scripts/feeds install -a

# 2. Install these packages (copy the whole directories into the buildroot)
cp -a xbslink-openwrt/openwrt/xbslinkd      package/
cp -a xbslink-openwrt/openwrt/luci-app-xbslink package/

# 3. Select the packages
make menuconfig    # Network -> Routing and Redirection -> xbslinkd
                   # LuCI -> Applications -> luci-app-xbslink

# 4. Build
make package/xbslinkd/compile V=s
make package/luci-app-xbslink/compile V=s
# artifacts: bin/packages/<arch>/*.ipk
```

> Requires the `golang` toolchain from `feeds/packages`; the first Go toolchain build is slow.

### Option B: plain cross-compilation (no SDK needed)

```sh
cd xbslink-openwrt
./build.sh mipsle        # or mips / armv7 / arm64 / 386 / amd64
# artifact: dist/xbslinkd.linux.mipsle etc.
```

Manual installation on the router (mipsle example):

```sh
scp dist/xbslinkd.linux.mipsle root@192.168.1.1:/usr/bin/xbslinkd
scp openwrt/xbslinkd/files/xbslink.config root@192.168.1.1:/etc/config/xbslink
scp openwrt/xbslinkd/files/xbslink.init  root@192.168.1.1:/etc/init.d/xbslink
ssh root@192.168.1.1 "chmod +x /usr/bin/xbslinkd /etc/init.d/xbslink \
  && /etc/init.d/xbslink enable && /etc/init.d/xbslink start"
```

### Option C: install prebuilt ipk (no compilation)

Prebuilt ipk files are included under `openwrt/ipk/` (regenerate any time with
`openwrt/build_ipk.sh`):

| File | Description |
| --- | --- |
| `luci-app-xbslink_0.9.7.5-1_all.ipk` | Web UI, architecture-independent (depends on xbslinkd) |
| `xbslinkd_0.9.7.5-1_<arch>.ipk` | Daemon + procd service + UCI config |
| `luci-i18n-xbslink-<lang>_0.9.7.5-1_all.ipk` | Optional UI translations (zh-cn / zh-tw / ja / ru) |

`<arch>` is the opkg architecture name and must match your firmware. Run
`opkg print-architecture` on the router to confirm it (common arm64 values:
`aarch64_cortex-a53` / `aarch64_cortex-a72` / `aarch64_generic`):

```sh
opkg install xbslinkd_0.9.7.5-1_aarch64_cortex-a53.ipk   # daemon first
opkg install luci-app-xbslink_0.9.7.5-1_all.ipk          # then the web UI
opkg install luci-i18n-xbslink-zh-cn_0.9.7.5-1_all.ipk   # optional: Simplified Chinese
```

After installing a translation, pick the language in LuCI "System -> System ->
Language and Style" and reload the page (other languages: `-zh-tw` / `-ja` / `-ru`).
The zh-Hans / zh-Hant wording is aligned with the official Chinese localization of the
Windows XBSlink (room name, direct remote host, listen port, "always forward" MAC list,
etc.). Translations are installed as `.lmo` files at
`/usr/lib/lua/luci/i18n/xbslink.<lang>.lmo`. Sources live in `po/<lang>/xbslink.po`;
rebuilding them requires LuCI's po2lmo tool (openwrt/luci repository, modules/luci-base/src):

```sh
PO2LMO=/path/to/po2lmo ./build_ipk.sh    # .lmo files are rebuilt automatically during the build
```

Once installed, configure under LuCI "Network -> XBSlink": tick Enable, pick the capture
interface (br-lan), fill in peer addresses, then save and restart the service. Note: the
menu depends on `/etc/config/xbslink`, so the xbslinkd package must be installed first.

> Troubleshooting: if `opkg install` reports `pkg_init_from_file: Malformed package file`,
> either the file got corrupted in transfer (verify with `md5sum`), or your opkg is too old
> to read the Debian ar format (the ipk files in this repo default to the legacy tar.gz
> format compatible with every opkg version; `IPK_FORMAT=ar ./build_ipk.sh` switches back
> to ar).

### Basic configuration

```sh
uci set xbslink.config.enabled='1'
uci set xbslink.config.interface='br-lan'     # kernel device name, see ip link show
uci set xbslink.config.nickname='living-room'
# Direct connection to friends' fixed nodes (multiple allowed, list syntax):
uci add_list xbslink.config.peers='1.2.3.4:31415'
uci commit xbslink
/etc/init.d/xbslink restart
```

Checking status:

```sh
cat /var/run/xbslinkd/status.json | jsonfilter -e @.nodes -i -   # or
curl 127.0.0.1:31416/status                                      # or
logread | grep xbslink
```

With `luci-app-xbslink` installed, view the live node table / latency / traffic and manage
the configuration on the **Network → XBSlink** page.

### Cloud matchmaking (automatic node discovery)

The original official cloud is offline. Since 0.9.7.5, the default cloud server points to
the private cloud-list service `http://10.165.0.105/cloudlist.php`; it can be changed in
LuCI or UCI. Two options:

1. **Self-host a cloud (recommended)**: deploy `cloudlist/index.php` to any PHP+MySQL host
   (original protocol, unmodified), then:
   ```sh
   uci set xbslink.config.cloud_enabled='1'
   uci set xbslink.config.cloud_server='http://your-server/cloudlist.php'
   uci set xbslink.config.cloud_name='my-room'
   ```
   When enabled, the init script automatically opens `udp/31415` on the WAN zone (the cloud
   server probes that port for reachability; once the probe passes you are registered as
   `reachable` and public nodes can connect directly).
2. **Pure static peers**: don't enable the cloud; interconnect via the `peers` list only —
   fine for small rooms of two or three players.

## How it works & protocol

For the complete wire-protocol specification (message formats, state machines, cloud API,
capture/injection filtering rules) see [docs/PROTOCOL.md](docs/PROTOCOL.md) —
reverse-engineered from the upstream C# source; it is the compatibility reference for this
port.

## Tests

* Unit tests (protocol codecs): `go test ./...`
* Interop regression against the original client (emulates the 1-byte datagram handshake of
  the Windows XBSlink GUI):
  ```sh
  go build -o /tmp/xbslinkd . && cd tests && unshare -Urn bash test_interop.sh
  ```
* Integration test (two instances + two emulated "hosts", broadcast/unicast round trip +
  loop prevention, run in a user namespace):
  ```sh
  go build -o /tmp/xbslinkd . && cd tests && unshare -Urn bash test_loop.sh
  ```
* Cloud client test (built-in fake cloudlist server, covering the full
  SENDHELLO/JOIN/UPDATE/LEAVE flow):
  ```sh
  unshare -Urn bash tests/test_cloud.sh
  ```

Current status: **interop 3/3, tunnel 7/7, cloud 5/5 passing**.
Notably, the interop tests cover a key wire behavior of the original GUI: when not joined
to a cloud, ANNOUNCE / GETNODELIST / GETCLIENTVERSION / GETNICKNAME are sent as 1-byte
datagrams (`getByteArray()` omits the length header when the payload is null), and
receivers accept datagrams of ≤3 bytes as zero-length payloads — strict length validation
would fail the handshake with the original.

## Known limitations & notes

* **Receivers on other subnets**: tunneled frames keep their original source IP. If the
  remote receiver is a Linux host with strict `rp_filter` (reverse-path validation)
  enabled, broadcasts from other subnets may be dropped. Xbox/PS3 consoles don't perform
  this check; only the router's own stack can be affected. Real Xbox system-link traffic
  uses source IP `0.0.0.1` and is unaffected by design.
* **Performance**: 100 Mbps-class system-link traffic (UDP 3074) is no problem for
  MT7621/MT7981-class CPUs; the hot path is a single memory copy + UDP send, with no
  encryption overhead.
* **IPv6**: the XBSlink protocol is IPv4-only; this port is the same.
* Cloud passwords travel as MD5 in cleartext (upstream protocol as-is), used only for
  cloud create/join validation.

## Directory layout

```
xbslink-openwrt/
├── main.go ...          Go sources (single package, module github.com/xbslink/xbslinkd, vendored deps)
├── message_test.go      protocol codec unit tests
├── build.sh             SDK-free cross-build script (6 architectures)
├── docs/
│   ├── PROTOCOL.md      wire protocol spec (reverse-engineered from the C# source)
│   └── BUILD.md         detailed build & install guide
├── openwrt/
│   ├── xbslinkd/        OpenWrt package (Makefile + UCI + procd init + src/ copy)
│   └── luci-app-xbslink/  LuCI web UI (JS views + menu + ACL)
├── cloudlist/index.php  self-hostable cloud node-list server (from upstream)
└── tests/               integration tests (test_loop.sh / test_cloud.sh / host emulators)
```

## License

The original XBSlink is GPL-2.0-or-later; this port is a derived, independent
implementation under the same license (see COPYING). `cloudlist/index.php` is taken from
the upstream repository.
