# SPDX-License-Identifier: GPL-2.0-or-later
#
# OpenWrt package for xbslinkd - a Go rewrite of the XBSlink system link proxy
#
# Build requirements:
#   - feeds/packages enabled (provides golang)
#
# Install this directory into your buildroot:  package/xbslinkd/
# then:  make menuconfig   (Network -> Routing and Redirection -> xbslinkd)
#        make package/xbslinkd/compile V=s

include $(TOPDIR)/rules.mk
include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

PKG_NAME:=xbslinkd
PKG_VERSION:=0.9.7.5
PKG_RELEASE:=1

PKG_MAINTAINER:=XBSlink OpenWrt port maintainers
PKG_LICENSE:=GPL-2.0-or-later
PKG_LICENSE_FILES:=COPYING

# The Go module sources live in ./src (copied into the buildroot together
# with this directory).
PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)-$(PKG_VERSION)

GO_PKG:=github.com/xbslink/xbslinkd
GO_PKG_BUILD_PKG:=$(GO_PKG)
# copy everything (vendor/ directory, go.mod, tests) into the build area
GO_PKG_INSTALL_ALL:=1
GO_PKG_LDFLAGS_X:=$(GO_PKG).appVersion=$(PKG_VERSION)

define Package/xbslinkd
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Routing and Redirection
  TITLE:=XBSlink system link proxy daemon (Go port)
  URL:=https://github.com/seuffert/XBSlink
endef

define Package/xbslinkd/description
  xbslinkd is a native Go rewrite of the XBSlink Xbox/PS3 system link
  proxy core, wire-protocol compatible with the original C# client.

  It captures system link broadcast/unicast frames on your LAN and
  tunnels them between remote XBSlink nodes (original XBSlink on
  Windows/Linux/Mac or other xbslinkd instances) via UDP, with optional
  node discovery through a cloudlist server (self-hostable PHP server
  included in the source distribution).

  Designed for OpenWrt: single static binary (~6 MB), UCI/procd
  integration, LuCI status page (luci-app-xbslink), no libpcap/cgo.
endef

define Package/xbslinkd/conffiles
/etc/config/xbslink
endef

define Build/Prepare
        mkdir -p $(PKG_BUILD_DIR)
        $(CP) ./src/. $(PKG_BUILD_DIR)/
endef

define Package/xbslinkd/install
        $(call GoPackage/Package/Install/Bin,$(1))
        $(INSTALL_DIR) $(1)/etc/config $(1)/etc/init.d
        $(INSTALL_CONF) ./files/xbslink.config $(1)/etc/config/xbslink
        $(INSTALL_BIN) ./files/xbslink.init $(1)/etc/init.d/xbslink
endef

$(eval $(call GoBinPackage,xbslinkd))
$(eval $(call BuildPackage,xbslinkd))
