#!/usr/bin/env bash
# Build sideload-able .ipk packages for xbslinkd and luci-app-xbslink
# WITHOUT the OpenWrt buildroot. Output goes to openwrt/ipk/.
#
# The .ipk container format (see IPK_FORMAT below):
#   legacy = tar.gz wrapper (works on ALL opkg releases, default)
#   ar     = Debian .deb style ar archive (needs opkg >= 0.4.2 / OpenWrt >= 21.02)
#
# Usage:
#   ./build_ipk.sh                    # default: the 3 common arm64 arches
#   ./build_ipk.sh aarch64_cortex-a53 # one specific opkg arch
#   ./build_ipk.sh mipsel_24kc x86_64 # several at once
#
# Check your router's architecture with:  opkg print-architecture
set -euo pipefail

VERSION=0.9.7.5
RELEASE=1
PKGVER="${VERSION}-${RELEASE}"
MAINTAINER="XBSlink OpenWrt port maintainers"
EPOCH=$(date +%s)

SRC="$(cd "$(dirname "$0")" && pwd)"          # .../openwrt
REPO="$(dirname "$SRC")"                      # .../xbslink-openwrt
DIST="$REPO/dist"
OUT="$SRC/ipk"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# ---------------------------------------------------------------- helpers

# opkg arch -> dist/ binary suffix
map_bin() {
        case "$1" in
                aarch64_*|armv8*)                 echo arm64 ;;
                arm_cortex-a5*|arm_cortex-a7*|arm_cortex-a9*|arm_cortex-a15*) echo armv7 ;;
                mipsel_*|mips64el*)               echo mipsle ;;
                mips_*|mips64*)                   echo mips ;;
                i386_*|i686*)                     echo 386 ;;
                x86_64)                           echo amd64 ;;
                *)                                echo "" ;;
        esac
}

# The .ipk container format:
#   legacy : outer = tar.gz wrapper holding ./debian-binary, ./control.tar.gz,
#            ./data.tar.gz - readable by EVERY opkg/ipkg release, old and new.
#            This is the format OpenWrt <=19.07 shipped and the only one old
#            opkg builds (some ImmortalWrt releases) accept; otherwise opkg
#            aborts with "pkg_init_from_file: Malformed package file".
#   ar     : outer = ar archive (Debian .deb style) - needs opkg >= 0.4.2
#            (OpenWrt >=21.02). Only use this if you know your opkg is new.
# Default: legacy, for maximum sideload compatibility.
IPK_FORMAT="${IPK_FORMAT:-legacy}"

tar_cz() { # $1=srcdir $2=outfile
        tar --sort=name --owner=0 --group=0 --numeric-owner \
            --mtime="@$EPOCH" -czf "$2" -C "$1" .
}

build_ipk() { # $1=staging dir  $2=output filename
        local stage="$1" out="$OUT/$2"
        local size
        size=$(du -sk "$stage/data" | cut -f1)
        echo "Installed-Size: ${size}" >> "$stage/CONTROL/control"
        tar_cz "$stage/CONTROL" "$WORK/control.tar.gz"
        tar_cz "$stage/data"    "$WORK/data.tar.gz"
        printf '2.0\n' > "$WORK/debian-binary"
        rm -f "$out"
        if [ "$IPK_FORMAT" = ar ]; then
                ar rc "$out" "$WORK/debian-binary" "$WORK/control.tar.gz" "$WORK/data.tar.gz"
        else
                tar --sort=name --owner=0 --group=0 --numeric-owner \
                    --mtime="@$EPOCH" -czf "$out" -C "$WORK" \
                    ./debian-binary ./control.tar.gz ./data.tar.gz
        fi
        echo "  -> $out ($IPK_FORMAT format)"
}

# ---------------------------------------------------------------- luci-app

build_luci() {
        local stage="$WORK/luci-app-xbslink"
        rm -rf "$stage"
        mkdir -p "$stage/CONTROL" \
                 "$stage/data/www/luci-static/resources/view/xbslink" \
                 "$stage/data/usr/share/luci/menu.d" \
                 "$stage/data/usr/share/rpcd/acl.d"

        install -m0644 "$SRC/luci-app-xbslink/htdocs/luci-static/resources/view/xbslink/overview.js" \
                "$stage/data/www/luci-static/resources/view/xbslink/overview.js"
        install -m0644 "$SRC/luci-app-xbslink/root/usr/share/luci/menu.d/luci-app-xbslink.json" \
                "$stage/data/usr/share/luci/menu.d/luci-app-xbslink.json"
        install -m0644 "$SRC/luci-app-xbslink/root/usr/share/rpcd/acl.d/luci-app-xbslink.json" \
                "$stage/data/usr/share/rpcd/acl.d/luci-app-xbslink.json"

        cat > "$stage/CONTROL/control" <<EOF
Package: luci-app-xbslink
Version: ${PKGVER}
Depends: luci-base, rpcd, xbslinkd
License: Apache-2.0
Section: luci
SourceDateEpoch: ${EPOCH}
Maintainer: ${MAINTAINER}
Architecture: all
Description: LuCI support for XBSlink (system link proxy)
 Web interface for xbslinkd: start/stop the service, edit node and cloud
 settings, and watch remote nodes, latency and tunneled traffic live.
EOF

        cat > "$stage/CONTROL/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] || {
        # register with the (legacy) ucitrack apply machinery as well, so
        # "Save & Apply" also reloads the service on older LuCI builds
        if ! uci -q show ucitrack 2>/dev/null | grep -q "\\.init='xbslink'"; then
                uci -q add ucitrack xbslink >/dev/null
                uci -q set ucitrack.@xbslink[-1].init='xbslink'
                uci -q commit ucitrack
        fi
        rm -f /tmp/luci-indexcache*
        rm -rf /tmp/luci-modulecache/
        killall -HUP rpcd 2>/dev/null
        exit 0
}
exit 0
EOF
        chmod 0755 "$stage/CONTROL/postinst"

        echo "building luci-app-xbslink_${PKGVER}_all.ipk"
        build_ipk "$stage" "luci-app-xbslink_${PKGVER}_all.ipk"
}

# ---------------------------------------------------------------- daemon

build_daemon() { # $1=opkg arch
        local arch="$1" binsuf
        binsuf=$(map_bin "$arch")
        if [ -z "$binsuf" ] || [ ! -f "$DIST/xbslinkd.linux.$binsuf" ]; then
                echo "!! no binary for arch '$arch' in dist/ - skipped" >&2
                return 1
        fi
        local stage="$WORK/xbslinkd"
        rm -rf "$stage"
        mkdir -p "$stage/CONTROL" \
                 "$stage/data/usr/bin" \
                 "$stage/data/etc/init.d" \
                 "$stage/data/etc/config"

        install -m0755 "$DIST/xbslinkd.linux.$binsuf" "$stage/data/usr/bin/xbslinkd"
        install -m0755 "$SRC/xbslinkd/files/xbslink.init"  "$stage/data/etc/init.d/xbslink"
        install -m0644 "$SRC/xbslinkd/files/xbslink.config" "$stage/data/etc/config/xbslink"

        echo "/etc/config/xbslink" > "$stage/CONTROL/conffiles"

        cat > "$stage/CONTROL/control" <<EOF
Package: xbslinkd
Version: ${PKGVER}
Depends: libc
License: GPL-2.0-or-later
Section: net
SourceDateEpoch: ${EPOCH}
Maintainer: ${MAINTAINER}
Architecture: ${arch}
Description: XBSlink system link proxy daemon
 Go rewrite of XBSlink, wire-compatible with the original Windows clients.
 Tunnels Xbox/PS3/PS4 system link LAN traffic between remote nodes over the
 internet. Pure Go, no libpcap needed; captures per bridge port.
EOF

        cat > "$stage/CONTROL/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] || {
        [ -x /etc/init.d/xbslink ] && /etc/init.d/xbslink enable
        exit 0
}
exit 0
EOF
        chmod 0755 "$stage/CONTROL/postinst"

        cat > "$stage/CONTROL/prerm" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] || /etc/init.d/xbslink stop 2>/dev/null
exit 0
EOF
        chmod 0755 "$stage/CONTROL/prerm"

        echo "building xbslinkd_${PKGVER}_${arch}.ipk (binary: xbslinkd.linux.$binsuf)"
        build_ipk "$stage" "xbslinkd_${PKGVER}_${arch}.ipk"
}

# ---------------------------------------------------------------- i18n

# Package the LuCI translations. Pre-compiled .lmo files live in
# luci-app-xbslink/po/lmo/ (one per language). If the PO2LMO env var points
# to a po2lmo binary (sources: openwrt/luci modules/luci-base/src), the
# .lmo files are re-compiled from po/<lang>/xbslink.po on every build.
build_i18n() {
        local po_root="$SRC/luci-app-xbslink/po"
        local pair srcdir lc pofile

        # map po dir name -> LuCI language code
        for pair in zh_Hans:zh-cn zh_Hant:zh-tw ja:ja ru:ru; do
                srcdir="${pair%%:*}"
                lc="${pair##*:}"
                pofile="$po_root/$srcdir/xbslink.po"

                if [ -n "${PO2LMO:-}" ] && [ -x "${PO2LMO}" ] && [ -f "$pofile" ]; then
                        echo "compiling $srcdir/xbslink.po -> xbslink.$lc.lmo"
                        "$PO2LMO" "$pofile" "$po_root/lmo/xbslink.$lc.lmo"
                fi

                if [ ! -f "$po_root/lmo/xbslink.$lc.lmo" ]; then
                        echo "!! missing prebuilt lmo for $lc - skipped" >&2
                        continue
                fi

                local stage="$WORK/luci-i18n-xbslink-$lc"
                rm -rf "$stage"
                mkdir -p "$stage/CONTROL" \
                         "$stage/data/usr/lib/lua/luci/i18n"

                install -m0644 "$po_root/lmo/xbslink.$lc.lmo" \
                        "$stage/data/usr/lib/lua/luci/i18n/xbslink.$lc.lmo"

                cat > "$stage/CONTROL/control" <<EOF
Package: luci-i18n-xbslink-$lc
Version: ${PKGVER}
Depends: luci-app-xbslink
License: GPL-2.0-or-later
Section: luci
SourceDateEpoch: ${EPOCH}
Maintainer: ${MAINTAINER}
Architecture: all
Description: LuCI translation for XBSlink ($lc)
EOF

                echo "building luci-i18n-xbslink-${lc}_${PKGVER}_all.ipk"
                build_ipk "$stage" "luci-i18n-xbslink-${lc}_${PKGVER}_all.ipk"
        done
}

# ---------------------------------------------------------------- main

mkdir -p "$OUT"
build_luci
build_i18n

if [ $# -gt 0 ]; then
        ARCHES=("$@")
else
        # default: the three opkg arch names used by common arm64 OpenWrt/
        # ImmortalWrt targets (ipq80xx/filogic/mt7622 = cortex-a53, RPi4/mvebu
        # = cortex-a72, armsr/generic = aarch64_generic)
        ARCHES=(aarch64_cortex-a53 aarch64_cortex-a72 aarch64_generic)
fi

rc=0
for a in "${ARCHES[@]}"; do
        build_daemon "$a" || rc=1
done

echo
echo "packages in $OUT:"
ls -la "$OUT"
exit $rc
