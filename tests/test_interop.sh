#!/bin/bash
# Interop regression test: original XBSlink GUI sends 1-byte datagrams for
# zero-payload messages (ANNOUNCE/GETCLIENTVERSION/GETNICKNAME ...). Verify
# that xbslinkd completes the handshake with such a client and shuts down
# cleanly. Run inside `unshare -Urn bash test_interop.sh`.
set -u
cd "$(dirname "$0")"

XBSBIN=${XBSBIN:-/tmp/xbslinkd}
RUN=/tmp/xbs-interop
PASS=0; FAIL=0
ok()  { echo "  [PASS] $1"; PASS=$((PASS+1)); }
bad() { echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }

clean() {
  pkill -f "xbslinkd --interface br-" 2>/dev/null
  sleep 0.3
  ip link del br-a 2>/dev/null
  rm -rf $RUN && mkdir -p $RUN
}
trap 'clean' EXIT
clean

ip link set lo up
ip link add br-a type bridge
ip link add ca type veth peer name c
ip link set ca master br-a
ip addr add 10.0.1.1/24 dev br-a
ip addr add 10.0.1.10/24 dev c
ip link set br-a up; ip link set ca up; ip link set c up

$XBSBIN --interface br-a --port 31415 --node-ip 10.0.1.1 --nickname NodeA \
  --status-port 0 --status-file $RUN/status.json \
  --verbose > $RUN/log.txt 2>&1 &
PID=$!
sleep 1
kill -0 $PID 2>/dev/null || { echo "xbslinkd died:"; tail -5 $RUN/log.txt; exit 1; }

echo "== 1. handshake with fake original GUI client =="
if python3 fake_original_client.py 10.0.1.1 31415 > $RUN/client.txt 2>&1; then
  ok "fake original client handshake complete"
  grep -E 'PASS|FAIL' $RUN/client.txt | sed 's/^/    /'
else
  bad "fake original client handshake failed"; cat $RUN/client.txt; tail -20 $RUN/log.txt
fi

echo "== 2. node visible in status =="
n=$(python3 -c "import json; s=json.load(open('$RUN/status.json')); ns=s.get('nodes',[]); print(len(ns), ns[0]['connected'] if ns else '-', ns[0].get('nickname') if ns else '-')" 2>/dev/null)
echo "    status: $n"
[ "${n%% *}" = "1" ] && ok "status shows 1 connected node" || bad "unexpected status: $n"

echo "== 3. clean shutdown (no 'shutdown timed out') =="
kill -TERM $PID
for i in $(seq 1 30); do kill -0 $PID 2>/dev/null || break; sleep 0.1; done
if kill -0 $PID 2>/dev/null; then
  bad "process still alive after SIGTERM"; kill -9 $PID
elif grep -q "shutdown timed out" $RUN/log.txt; then
  bad "log contains 'shutdown timed out'"
else
  ok "clean shutdown"
fi

echo
echo "================================"
echo "RESULT: $PASS passed, $FAIL failed"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
