# 构建与安装详解

## 前置条件

| 方式 | 需要 | 产物 |
|---|---|---|
| A. OpenWrt SDK | golang feed（首次编译 Go 工具链约 10-30 分钟） | 可安装的 ipk，含 init/uci |
| B. 独立交叉编译 | 本机 Go ≥ 1.21（无 cgo，任何发行版自带即可） | 静态二进制，手工安装 |

> xbslinkd 是纯 Go + 已 vendor 的依赖（`golang.org/x/sys`），两种方式均**无需联网**。

## 方式 A：OpenWrt SDK 构建

1. 从 https://downloads.openwrt.org/ 下载与你固件**同版本同架构**的 SDK，解压并进入目录。

2. 启用 feeds（需要外网或镜像）：

   ```sh
   ./scripts/feeds update -a
   ./scripts/feeds install -a
   ```

   验证 golang 可用：`make package/feeds/packages/golang/host/compile` 可跳过，
   编译 xbslinkd 时会自动作为 PKG_BUILD_DEPENDS 安装。

3. 复制包目录（注意包名目录）：

   ```sh
   cp -a <本仓库>/openwrt/xbslinkd       package/xbslinkd
   cp -a <本仓库>/openwrt/luci-app-xbslink package/luci-app-xbslink
   ```

   目录布局要求：

   ```
   package/xbslinkd/
   ├── Makefile
   ├── files/
   │   ├── xbslink.config
   │   └── xbslink.init
   └── src/                 ← Go 模块源码（go.mod、*.go、vendor/）
   ```

   发布 tarball 中 `openwrt/xbslinkd/src/` 已就位；若你单独拷贝 Makefile，
   请同时把仓库根部的 Go 源码复制为 `src/`。

4. 选择并编译：

   ```sh
   make menuconfig
   #   Network  -> Routing and Redirection -> xbslinkd        [M]
   #   LuCI     -> Applications -> luci-app-xbslink            [M]
   make package/xbslinkd/compile V=s
   make package/luci-app-xbslink/compile V=s
   ```

   产物位置：

   ```
   bin/packages/<你的架构>/xbslinkd_0.9.7.5-1_<架构>.ipk
   bin/packages/<你的架构>/luci-app-xbslink_0.9.7.5-1_all.ipk
   ```

5. 安装到路由器：

   ```sh
   opkg install xbslinkd_*.ipk luci-app-xbslink_*.ipk
   ```

### 24.10 / master 差异

本包按 23.05 的 `golang-package.mk` 约定编写，在 24.10 同样适用（宏接口未变）。
若在 master 上遇到 `GoPackage/Package/Install/Bin` 报错，确认
`feeds/packages/lang/golang` 已更新。

### 常见问题

* `find ... golang-package.mk: No such file` → feeds/packages 未启用或未安装：
  `./scripts/feeds install golang`。
* 首次编译报内存不足 → Go 工具链编译需要 ≥2GB 可用内存/交换。
* `go: cannot find main module` → `src/` 没有随包复制（检查 `src/go.mod`）。

## 方式 B：独立交叉编译

```sh
./build.sh mipsle     # mips|armv7|arm64|386|amd64 可选
```

脚本内部即 `GOOS=linux GOARCH=<arch> GOMIPS=softfloat go build -trimpath -ldflags "-s -w"`。
产物在 `dist/`。

### 手动安装

```sh
scp dist/xbslinkd.linux.mipsle root@<路由器>:/usr/bin/xbslinkd
scp openwrt/xbslinkd/files/xbslink.config root@<路由器>:/etc/config/xbslink
scp openwrt/xbslinkd/files/xbslink.init  root@<路由器>:/etc/init.d/xbslink
ssh root@<路由器> '
  chmod +x /usr/bin/xbslinkd /etc/init.d/xbslink
  /etc/init.d/xbslink enable
  /etc/init.d/xbslink start
  logread -f | grep xbslink    # 观察启动日志
'
```

命令行与原版 XBSlink 控制台参数兼容（`-i/-n/-p/-s/-c/-w/-m`），也可不装 UCI 直接运行：

```sh
xbslinkd -i br-lan -n 路由昵称 -c 房间名 -s http://cloud.example.com/cloudlist/ --verbose
```

完整选项：`xbslinkd --help`。

## 架构对照表

| 路由器 CPU | GOARCH | 说明 |
|---|---|---|
| MT7620/7621/7628 (ramips) | mipsle（softfloat） | 新3/小米 mini/PandoraBox 等 |
| Atheros ar71xx/ath79 | mips（softfloat） | 经典 TP-Link WR 系列 |
| ipq40xx/ipq806x (ARM) | armv7 | 网件 R 系列、小米 R3Gv2 |
| filogic/mt798x (ARM64) | arm64 | 红米 AX6000、小米 AX3000T |
| x86 软路由 | amd64 | N1 盒子用 arm64 |
| 老式 MIPS64 | mips64（需自行编译） | 少见 |

## 自建 cloudlist 云服务器

1. 任意 PHP ≥ 7 + MySQL 主机，部署 `cloudlist/index.php`；
2. 按文件头注释修改 `DB_*` 常量（表会自动创建）；
3. 客户端配置 `cloud_server` 指向其 URL 即可。

服务器行为（与上游一致）：节点 70 秒无 UPDATE 即过期；JOIN 时向
`node_ip:node_port` 发 UDP PING 验证可达；`node_ip` 为私网/0.0.0.0 时以
HTTP 来源 IP 代替。
