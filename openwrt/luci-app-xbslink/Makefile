# SPDX-License-Identifier: GPL-2.0-or-later
#
# LuCI app for xbslinkd. Requires the luci feed.

include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-xbslink
PKG_VERSION:=1.0.0
PKG_RELEASE:=1

PKG_MAINTAINER:=XBSlink OpenWrt port maintainers
PKG_LICENSE:=Apache-2.0

LUCI_TITLE:=LuCI support for XBSlink (system link proxy)
LUCI_DEPENDS:=+luci-base +xbslinkd
LUCI_PKGARCH:=all

define Package/$(PKG_NAME)/description
  Web interface for xbslinkd: start/stop the service, edit node and cloud
  settings, and watch remote nodes, latency and tunneled traffic live.
endef

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - in luci.mk context
$(eval $(call BuildPackage,luci-app-xbslink))
