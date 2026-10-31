# xbslinkd — XBSlink 的 OpenWrt 移植（Go 重写）

将 [seuffert/XBSlink](https://github.com/seuffert/XBSlink)（C# / .NET，Xbox 360 & PS3 系统联机代理）
移植到 OpenWrt 的成果：**xbslinkd** 是一个无依赖、纯 Go（无 cgo/libpcap）的守护进程，
与原版 XBSlink 客户端**线上协议完全兼容**（原版 0.9.6.0），可与之直接组网联机。

```
┌─────────────┐   br-lan 端口抓包    ┌──────────────┐  UDP 31415   ┌──────────────┐  注入    ┌─────────────┐
│ Xbox / PS3  │ ──────────────────► │  OpenWrt     │ ───────────► │ 远端节点      │ ───────► │ Xbox / PS3  │
│  (本网段)    │ ◄────────────────── │  xbslinkd    │ ◄─────────── │ XBSlink 原版  │ ◄─────── │  (远端网段)  │
└─────────────┘                     │  (路由器上)   │  或其他实例    │  / xbslinkd  │          └─────────────┘
                                    └──────┬───────┘              └──────┬───────┘
                                           │ HTTP                        │
                                    ┌──────┴─────────┐                    │
                                    │ cloudlist 云端  │◄───────────────────┘
                                    │ (可自建, 见下)   │   节点发现 / 注册
                                    └────────────────┘
```

## 特性

* **协议兼容**：按上游 C# 源码逐字节复刻节点间 UDP 协议（`[类型][长度LE][载荷]`）与
  cloudlist HTTP 协议（JOIN/UPDATE/LEAVE/SENDHELLO，29s 心跳、MD5 云口令、
  私网 IP 由服务器替换为公网来源 IP、SERVERHELLO 端口可达性探测）。
* **核心隧道**：以太网帧级转发。广播帧泛洪至所有远端节点，单播帧按"从远端学习到的
  目的 MAC"定向路由；防回环（注入 MAC 去重窗口）；网关 IP / 特权端口过滤与原版一致。
* **抓包**：Linux `AF_PACKET` + 内核 BPF 过滤（`广播 OR (IP 且 UDP)`），用户态按
  原版规则精确筛选：Xbox 签名（源 IP `0.0.0.1`）、UDP 端口 3074-3075 / 14000-14001、
  可选高端口广播转发、可选指定 MAC 白名单。
  **在 br-lan 网桥上自动改为逐端口抓包**（网桥 master 抓不到端口间单播帧，这是
  libpcap 方案的原版也同样存在的坑，本移植已解决）。
* **云节点发现**：客户端完整实现；官方 `secudb.de` 云已停服，附带可自建的
  `cloudlist/index.php`（与上游一致，MySQL 存储）。
* **OpenWrt 集成**：UCI 配置（`/etc/config/xbslink`）、procd 服务与开机自启、
  自动防火墙 WAN 口规则、LuCI 状态/配置页面（`luci-app-xbslink`）。
* **状态输出**：JSON 状态文件（LuCI 读取）+ `127.0.0.1:31416/status` HTTP 端点。
* **体积**：静态二进制 ~5-6 MB（6 架构全支持），运行内存 ~10-20 MB。

## 未移植的功能（与原版差异）

| 原版功能 | 状态 | 说明 |
|---|---|---|
| GUI / 聊天 / 提示音 | 不适用 | 路由器无头运行；聊天消息仍可接收并写入日志 |
| UPnP 端口映射 | 不需要 | 路由器本身就是网关，init 脚本直接开防火墙规则 |
| NAT 模式（多台同网段主机 MAC 冲突改写） | 未实现 | 单路由器场景不需要；协议层留有兼容钩子 |
| Windows 原始套接字抓包 | 不适用 | Linux AF_PACKET 替代 |

## 快速开始

### 方式 A：OpenWrt SDK 编译 ipk（推荐，23.05 / 24.10）

```sh
# 1. 下载对应架构的 SDK 并解压，进入 SDK 目录
./scripts/feeds update base packages luci
./scripts/feeds install -a

# 2. 安装本包（整个目录拷进 buildroot）
cp -a xbslink-openwrt/openwrt/xbslinkd      package/
cp -a xbslink-openwrt/openwrt/luci-app-xbslink package/

# 3. 选择包
make menuconfig    # Network -> Routing and Redirection -> xbslinkd
                   # LuCI -> Applications -> luci-app-xbslink

# 4. 编译
make package/xbslinkd/compile V=s
make package/luci-app-xbslink/compile V=s
# 产物: bin/packages/<arch>/*.ipk
```

> 需要 `feeds/packages` 提供 `golang` 工具链；首次编译 Go 工具链较慢。

### 方式 B：直接交叉编译（无需 SDK）

```sh
cd xbslink-openwrt
./build.sh mipsle        # 或 mips / armv7 / arm64 / 386 / amd64
# 产物: dist/xbslinkd.linux.mipsle 等
```

手工安装到路由器（以 mipsle 为例）：

```sh
scp dist/xbslinkd.linux.mipsle root@192.168.1.1:/usr/bin/xbslinkd
scp openwrt/xbslinkd/files/xbslink.config root@192.168.1.1:/etc/config/xbslink
scp openwrt/xbslinkd/files/xbslink.init  root@192.168.1.1:/etc/init.d/xbslink
ssh root@192.168.1.1 "chmod +x /usr/bin/xbslinkd /etc/init.d/xbslink \
  && /etc/init.d/xbslink enable && /etc/init.d/xbslink start"
```

### 基本配置

```sh
uci set xbslink.config.enabled='1'
uci set xbslink.config.interface='br-lan'     # 内核设备名，ip link show 查看
uci set xbslink.config.nickname='客厅路由'
# 与朋友的固定节点直连（可多个，list 语法）：
uci add_list xbslink.config.peers='1.2.3.4:31415'
uci commit xbslink
/etc/init.d/xbslink restart
```

状态查看：

```sh
cat /var/run/xbslinkd/status.json | jsonfilter -e @.nodes -i -   # 或
curl 127.0.0.1:31416/status                                      # 或
logread | grep xbslink
```

安装 `luci-app-xbslink` 后在 **网络 → XBSlink** 页面查看实时节点表/延迟/流量并管理配置。

### 云端联机（节点自动发现）

原版官方云已下线，两种选择：

1. **自建云**（推荐）：把 `cloudlist/index.php` 部署到任意 PHP+MySQL 主机
   （原版协议，无修改），然后：
   ```sh
   uci set xbslink.config.cloud_enabled='1'
   uci set xbslink.config.cloud_server='http://your-server/cloudlist/'
   uci set xbslink.config.cloud_name='我的房间'
   ```
   启用时 init 脚本自动在 WAN 区打开 `udp/31415`（云服务器会探测该端口可达性，
   探测通过后注册为 `reachable`，公网节点可直连）。
2. **纯静态节点**：不启用云，只用 `peers` 列表互连，适合两三人小房间。

## 工作原理与协议

完整的线上协议规格（消息格式、状态机、云 API、抓包/注入过滤规则）见
[docs/PROTOCOL.md](docs/PROTOCOL.md)——逆向自上游 C# 源码，是本移植的兼容性依据。

## 测试

* 单元测试（协议编解码）：`go test ./...`
* 原版客户端互操作回归（模拟 Windows XBSlink GUI 的 1 字节数据报握手）：
  ```sh
  go build -o /tmp/xbslinkd . && cd tests && unshare -Urn bash test_interop.sh
  ```
* 集成测试（双实例 + 双"主机"广播/单播闭环 + 防回环，在用户命名空间运行）：
  ```sh
  go build -o /tmp/xbslinkd . && cd tests && unshare -Urn bash test_loop.sh
  ```
* 云客户端测试（内置假 cloudlist 服务器，覆盖 SENDHELLO/JOIN/UPDATE/LEAVE 全流程）：
  ```sh
  unshare -Urn bash tests/test_cloud.sh
  ```

当前状态：**互操作测试 3/3、隧道测试 7/7、云测试 5/5 通过**。
其中互操作测试覆盖了原版 GUI 的一个关键线上行为：未加入云时，ANNOUNCE /
GETNODELIST / GETCLIENTVERSION / GETNICKNAME 均以 1 字节数据报发送
（`getByteArray()` 载荷为 null 时省略长度头），接收端对 ≤3 字节数据报按零载荷
接受——严格校验长度将无法与原版握手。

## 已知限制与注意事项

* **跨子网接收方**：隧道德帧保留原始源 IP。若远端接收方是 Linux 且开启
  `rp_filter`（严格反向路径校验），来自其他子网的广播可能被丢弃。
  Xbox/PS3 主机不做此校验；路由器本机接收才会遇到。真实 Xbox 联机流量源 IP 为
  `0.0.0.1`，天然不受影响。
* **性能**：100 Mbps 级联机流量（UDP 3074）对 MT7621/MT7981 级别 CPU 无压力；
  核心路径为单次内存拷贝 + UDP 发送，无加密开销。
* **IPv6**：XBSlink 协议本身仅 IPv4，本移植一致。
* 云口令以 MD5 明文传输（上游协议如此），仅用于云创建/加入校验。

## 目录结构

```
xbslink-openwrt/
├── main.go ...          Go 源码（单包，模块 github.com/xbslink/xbslinkd，已 vendor 依赖）
├── message_test.go      协议编解码单元测试
├── build.sh             无 SDK 交叉编译脚本（6 架构）
├── docs/
│   ├── PROTOCOL.md      线上协议规格（逆向自 C# 源码）
│   └── BUILD.md         构建与安装详解
├── openwrt/
│   ├── xbslinkd/        OpenWrt 包（Makefile + UCI + procd init + src/ 副本）
│   └── luci-app-xbslink/  LuCI 网页（JS 视图 + 菜单 + ACL）
├── cloudlist/index.php  可自建的云节点列表服务器（上游原版）
└── tests/               集成测试（test_loop.sh / test_cloud.sh / 主机模拟器）
```

## 许可证

XBSlink 原版为 GPL-2.0-or-later；本移植是衍生的独立实现，沿用同一许可证（见 COPYING）。
`cloudlist/index.php` 取自上游仓库。
