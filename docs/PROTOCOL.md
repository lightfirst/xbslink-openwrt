# XBSlink 线缆协议规格（逆向自上游 C# 源码）

> 本文记录从 XBSlink v0.9.6.0（seuffert/XBSlink）C# 源码中提取的完整线上协议，
> 是 Go 重写版（xbslinkd）与原版保持互操作性的依据。参考文件：`xbs_node_message.cs`、
> `xbs_udp_listener.cs`、`xbs_sniffer.cs`、`xbs_node.cs`、`xbs_node_list.cs`、
> `xbs_cloudlist.cs`、`cloudlist/index.php`。

## 1. 节点间 UDP 消息（默认端口 31415，可配置）

每个数据报为一条消息：

```
偏移  长度  说明
0     1    消息类型（见下表）
1     2    载荷长度 data_len（小端 UInt16）
3     N    载荷
```

校验规则（原版 `xbs_udp_listener.cs:190`）：**仅当数据报长度 > 3 字节时**才校验
`data_len + 3 == 整包长度`；1~3 字节的数据报一律按“零载荷消息”接受（`data_len=0`）。

> **互操作关键**：原版发送端在载荷为 null 时（`getByteArray()`，`data == null`）整个
> 消息**只有 1 个字节**（仅类型字节，无长度头）。未加入云的原版 GUI 点
> "connect to remote host" 发的就是 1 字节 ANNOUNCE；GETNODELIST /
> GETCLIENTVERSION / GETNICKNAME 同样是 1 字节数据报。严格按
> `len+3==整包` 校验将无法与原版客户端握手（v1.0.0 之前的移植版即踩此坑）。

### 消息类型

| 值  | 名称               | 载荷格式                                   |
|-----|--------------------|--------------------------------------------|
|0x00 | ANNOUNCE          | options 字符串（见下），可为空              |
|0x01 | GETNODELIST       | 无                                          |
|0x02 | ADDNODE           | 4B IPv4 + 2B 端口（小端）                   |
|0x03 | DELNODE           | 同 ADDNODE                                  |
|0x04 | PING              | 8B .NET DateTime.Ticks（小端，仅回显用）    |
|0x05 | PONG              | 原样回显 PING 的 8B                         |
|0x06 | DATA              | 完整以太网帧（前 6B 目的 MAC，6B 源 MAC）   |
|0x07 | ADDMASTERBROWSER  | 已废弃                                      |
|0x08 | GETCLIENTVERSION  | 无                                          |
|0x09 | CLIENTVERSION     | 4B 版本号 [主,次,修订,编译]（如 0.9.6.0）   |
|0x0A | CHATMSG           | UTF-8 字符串                                |
|0x0B | NICKNAME          | UTF-8 字符串                                |
|0x0C | GETNICKNAME       | 无                                          |
|0x0D | KNOWNNODE         | 同 ADDNODE                                  |
|0x0E | TO_CLOUDHELPER_HELPWITHNODE | 同 ADDNODE（目标节点地址）        |
|0x0F | FROM_CLOUDHELPER_CONTACTNODE| 同 ADDNODE（需联系的节点地址）    |
|0xFF | SERVERHELLO       | 空（云服务器发来的端口可达性探测）          |

### ANNOUNCE options 字符串

键值对 `key<TAB(0x09)>value`，多个键值对之间以 `NUL(0x00)` 分隔（无结尾分隔符）。
已知键：`CLOUD` = 云名称（加入同一云的节点才会互相应答）。

## 2. 节点发现 / 保活状态机（`xbs_node_list.cs`）

* **主动连接**：向目标 `ip:port` 发 ANNOUNCE（带 CLOUD 选项，若有）→
  对方若（同云/无云）回 ADDNODE(其 local ip:port) → 双方加入各自节点表，
  随后互发 GETCLIENTVERSION / PING / GETNICKNAME。
* **节点表交换**：收到陌生节点的 ADDNODE 后，回自己的 ADDNODE，并（不在云中时）
  将全部已知节点以 KNOWNNODE 消息发给新节点。
* **PING 循环**（每秒 tick）：
  * 距上次 PING 或上次 PONG > 20s → 发 PING；
  * 距上次 PONG > 30s → 发 DELNODE(自己) 给该节点并从表中删除；
  * 秒数逢偶数时补发缺失的 nickname / clientversion / ping 查询。
* **添加超时**：ANNOUNCE 后 2s 未确认 → 请求随机已知节点做"云中继"
  （TO_CLOUDHELPER → 对方向目标发 FROM_CLOUDHELPER 促其回连），最多 3 次。
* **DATA 分发**（`distributeDataPacket`）：
  * 目的 MAC 为广播 `ff:ff:ff:ff:ff:ff` 或零 `00:00:00:00:00:00` → 发给**所有**节点；
  * 单播 → 只发给"已登记该目的 MAC 为其 xbox"的节点。

## 3. 抓包与注入（`xbs_sniffer.cs`，libpcap）

### 抓包过滤器（BPF，动态拼接）

包含：
* 游戏主机签名：`udp and ((ip host 0.0.0.1) or (dst portrange 3074-3075) or (dst portrange 14000-14001))`
* 可选（转发全部高端口广播）：`ether dst ff:ff:ff:ff:ff:ff and udp and dst portrange 1024-65535`
* 用户指定的特殊 MAC：`ether src <mac> ...`

排除：
* 本机抓包网卡自身 MAC
* 网关 IP（源或目的）
* `ip and (dst portrange 1-1023 or src portrange 1-1023)`
* 远端注入帧的源 MAC（防回环，`injected_macs`）

### 注入（收到远端 DATA 时）

1. 将帧源 MAC 记入 `injected_macs`（**先记录后发送**，防回环）；
2. 记录源 MAC 为发送节点的 xbox（用于单播回程路由）；
3. 过滤：ARP/IP 源或目的为网关 IP → 丢弃；UDP/TCP 目的端口 <1024 → 丢弃；
4. 原始二层帧直接写入 LAN。

## 4. 云（cloudlist）HTTP 协议（`xbs_cloudlist.cs` + `index.php`）

本移植的默认 URL 为 `http://10.165.0.105/cloudlist.php`（私有云列表服务）。原版默认
`http://www.secudb.de/~seuffert/xbslink/cloudlist/` 官方服务器现已停服，可自建：本仓库
`cloudlist/index.php`。全部为 HTTP GET，响应为纯文本行，
首行 `OK:...` 或 `ERROR:...`。服务器节点超时 70s，客户端每 **29s** UPDATE 一次。

| 命令 | GET 参数 | 响应 |
|------|----------|------|
| GETLIST | cmd | `OK:` + 每行 `cloudname=..&countnodes=..&maxnodes=..&password=True/False` |
| JOIN | cmd, cloudname, maxnodes, password(MD5 大写 hex), node_ip, node_port, nick, reachable(0/1), getallnodes=1, clientversion | `OK:<uuid>` + 每行 `node_ip=<ip>&node_port=<port>`（云中其他节点）|
| UPDATE | cmd, cloudname, uuid | `OK:<uuid>` + 同上（全部其他节点）|
| LEAVE | cmd, cloudname, uuid | `OK:` |
| SENDHELLO | cmd, node_ip, node_port | `OK:`（并向 node_ip:node_port 发 2 个 SERVERHELLO UDP 包）|

* **node_ip 为私网地址或 0.0.0.0 时**，服务器以 HTTP 来源 IP（REMOTE_ADDR）代替——
  因此客户端无需知道自己的公网 IP；
* JOIN 时服务器会向 `node_ip:node_port` 发 UDP PING（载荷 4B/8B 时间戳，两试、1s 超时），
  收到**任意**回复即标记 reachable；节点端按普通 PING 处理（回 PONG 即可）；
* 客户端在加入前可先调 SENDHELLO 并等待 SERVERHELLO 判定端口可达性；
* uuid = `time()+node_ip+node_port`（服务器生成，仅回传使用）；
* clientversion 按去点字符串比较，服务器要求 ≥ `0953`。

## 5. 与原版互操作注意事项

* 多字节整数一律**小端**（C# BitConverter 在 x86 上为小端）；
* PING 载荷内容不参与逻辑，PONG 必须原样回显（服务器兼容依赖此点）；
* DATA 帧长上限 65535-3 字节（UInt16 限制），普通以太网 1500 MTU 无压力；
* 原版无加密、无鉴权（云密码仅用于云创建，MD5 后明文传输）；
* 版本号建议上报 `0.9.6.0` 以获得最大兼容性。
