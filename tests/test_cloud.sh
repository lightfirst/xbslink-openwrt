#!/bin/bash
# Cloud client test: fake cloudlist server + xbslinkd cloud mode.
set -u
cd "$(dirname "$0")"
RUN=/tmp/xbs-cloud
rm -rf $RUN && mkdir -p $RUN
PASS=0; FAIL=0
ok()  { echo "  [PASS] $1"; PASS=$((PASS+1)); }
bad() { echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }

ip link set lo up 2>/dev/null
echo "== start fake cloud server =="
python3 fake_cloud.py 8099 &
SRV=$!
sleep 0.5

echo "== start xbslinkd with cloud enabled =="
/tmp/xbslinkd --interface lo --port 31415 --node-ip 10.0.1.1 --nickname CloudNode \
  --cloudserver http://127.0.0.1:8099/ --cloudname testcloud --password secret123 \
  --max-nodes 16 --status-port 0 --status-file $RUN/status.json \
  --verbose > $RUN/xbs.log 2>&1 &
XPID=$!
sleep 3

echo "== check 1: SENDHELLO probe -> port reachable =="
if grep -qi "reachable from the internet" $RUN/xbs.log; then ok "port reachability probe succeeded"; else bad "no reachability confirmation"; fi

echo "== check 2: JOIN accepted, uuid received =="
if grep -q "joined cloud" $RUN/xbs.log; then ok "cloud joined"; else bad "join failed"; tail -6 $RUN/xbs.log; fi

echo "== check 3: server received correct params =="
sleep 1
if grep -q "cloudname=testcloud" /tmp/fakecloud.log && \
   grep -q "clientversion=0.9.6.0" /tmp/fakecloud.log && \
   grep -q "password=5D7845AC6EE7CFFFAFC5FE5F35CF666D" /tmp/fakecloud.log && \
   grep -q "nick=CloudNode" /tmp/fakecloud.log; then
  ok "JOIN params + MD5 password correct"
else
  bad "JOIN request malformed"
fi

echo "== check 4: UPDATE discovers phantom node =="
for i in $(seq 1 12); do
  if grep -q "new cloud node 203.0.113.77:31415" $RUN/xbs.log; then found=1; break; fi
  sleep 3
done
if [ "${found:-0}" = "1" ]; then ok "phantom node discovered via UPDATE"; else bad "phantom node not discovered"; fi

echo "== check 5: graceful LEAVE on shutdown =="
kill $XPID
sleep 2
if grep -q "left cloud" $RUN/xbs.log; then ok "LEAVE sent on shutdown"; else bad "no LEAVE"; fi

echo "== server request log =="
grep REQ /tmp/fakecloud.log | head -6
kill $SRV 2>/dev/null
echo
echo "RESULT: $PASS passed, $FAIL failed"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
