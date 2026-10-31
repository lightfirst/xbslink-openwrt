#!/bin/sh
# Cross-compile xbslinkd without the OpenWrt SDK.
# Usage: ./build.sh [mips|armv7|arm64|386|amd64|all]
set -e
cd "$(dirname "$0")"

TARGETS="mipsle mips armv7 arm64 386 amd64"
[ "$1" != "all" ] && [ -n "$1" ] && TARGETS="$1"

mkdir -p dist
for t in $TARGETS; do
    case "$t" in
        mipsle)  export GOOS=linux GOARCH=mipsle GOMIPS=softfloat ;;
        mips)    export GOOS=linux GOARCH=mips GOMIPS=softfloat ;;
        armv7)   export GOOS=linux GOARCH=arm GOARM=7 ;;
        arm64)   export GOOS=linux GOARCH=arm64 ;;
        386)     export GOOS=linux GOARCH=386 ;;
        amd64)   export GOOS=linux GOARCH=amd64 ;;
        *) echo "unknown target: $t" >&2; exit 1 ;;
    esac
    echo "== building $t =="
    go build -trimpath -ldflags "-s -w" -o "dist/xbslinkd.linux.$t" .
    unset GOOS GOARCH GOARM GOMIPS
done
echo "done:" && ls -la dist/
