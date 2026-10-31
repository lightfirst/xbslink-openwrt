#!/bin/bash
# xbslinkd integration test: two instances tunneling LAN broadcasts.
#
# Topology (single user+network namespace, run via `unshare -Urn`):
#
#   console-a (10.0.1.10)                     console-b (10.0.2.10)
#        | ca <-> ca-br                              cb <-> cb-br |
#        +---------- br-a (10.0.1.1)                 +----- br-b (10.0.2.1)
#                        |                                 |
#                   xbslinkd A :31415  <-- loopback -->  xbslinkd B :31416
#                   capture br-a                          capture br-b
#
# Checks: node handshake + RTT, broadcast tunneling both directions,
# unicast tunneling (MAC learning), loop prevention.
set -u
cd "$(dirname "$0")"

XBSBIN=${XBSBIN:-/tmp/xbslinkd}
RUN=/tmp/xbs-test
PASS=0
FAIL=0

ok()  { echo "  [PASS] $1"; PASS=$((PASS+1)); }
bad() { echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }

KEEP=${KEEP:-0}
clean() {
  pkill -f "xbslinkd --interface br-" 2>/dev/null
  sleep 0.3
  ip link del br-a 2>/dev/null
  ip link del br-b 2>/dev/null
  if [ "$KEEP" = "1" ]; then mkdir -p $RUN; return; fi
  rm -rf $RUN && mkdir -p $RUN
}
trap 'clean' EXIT

echo "== setup topology =="
clean
ip link set lo up
for side in a b; do
  ip link add br-$side type bridge
  ip link add ca-$side type veth peer name c$side
  ip link set ca-$side master br-$side
  ip addr add 10.0.$([ $side = a ] && echo 1 || echo 2).1/24 dev br-$side
  ip addr add 10.0.$([ $side = a ] && echo 1 || echo 2).10/24 dev c$side
  ip link set br-$side up; ip link set ca-$side up; ip link set c$side up
done
echo "  topology ready"

get_mac() { ip -o link show "$1" | sed -n 's/.*ether \([0-9a-f:]*\).*/\1/p'; }
MAC_CA=$(get_mac ca)
MAC_CB=$(get_mac cb)
echo "  console-a MAC: $MAC_CA, console-b MAC: $MAC_CB"

echo "== start xbslinkd instances =="
$XBSBIN --interface br-a --port 31415 --node-ip 10.0.1.1 --nickname NodeA \
  --peers 127.0.0.1:31416 --status-port 0 --status-file $RUN/status-a.json \
  --verbose > $RUN/log-a.txt 2>&1 &
PID_A=$!
$XBSBIN --interface br-b --port 31416 --node-ip 10.0.2.1 --nickname NodeB \
  --status-port 0 --status-file $RUN/status-b.json \
  --verbose > $RUN/log-b.txt 2>&1 &
PID_B=$!
sleep 1
kill -0 $PID_A 2>/dev/null || { echo "  xbslinkd A died:"; tail -5 $RUN/log-a.txt; exit 1; }
kill -0 $PID_B 2>/dev/null || { echo "  xbslinkd B died:"; tail -5 $RUN/log-b.txt; exit 1; }
ok "both instances running"

echo "== check 1: node handshake =="
handshake_ok=0
for i in $(seq 1 15); do
  n=$(python3 -c "
import json
try:
  s=json.load(open('$RUN/status-a.json'))
  nodes=s.get('nodes',[])
  conn=[x for x in nodes if x.get('connected')]
  print(len(conn), conn[0].get('ping_ms',-1) if conn else -1)
except Exception as e:
  print('0 err')
" 2>/dev/null)
  cnt=$(echo $n | cut -d' ' -f1)
  ping=$(echo $n | cut -d' ' -f2)
  if [ "$cnt" = "1" ] && [ "$ping" != "-1" ] && [ -n "$ping" ]; then handshake_ok=1; break; fi
  sleep 1
done
if [ "$handshake_ok" = "1" ]; then ok "nodes connected, RTT measured: ${ping}ms"; else bad "handshake failed (status: $n)"; tail -20 $RUN/log-a.txt $RUN/log-b.txt; exit 1; fi

echo "== check 2: broadcast tunnel A -> B =="
python3 raw_recorder.py cb 11 > $RUN/raw-cb.txt 2>&1 &
python3 console_listen.py 3074 10 XBS-TEST > $RUN/listen-b.json 2>&1 &
LPID=$!
sleep 0.5
python3 console_send.py ca $MAC_CA 0.0.0.1 bcast 3 >/dev/null
wait $LPID
recv=$(tail -1 $RUN/listen-b.json | python3 -c "import json,sys; print(json.load(sys.stdin).get('total',0))")
tok=$(tail -1 $RUN/listen-b.json | python3 -c "import json,sys; print(json.load(sys.stdin).get('tag_ok',0))")
if [ "$tok" -ge 3 ]; then ok "console-b received $tok/3 broadcasts through tunnel"; else bad "console-b received $tok/3 ($recv raw)"; cat $RUN/listen-b.json; fi
echo "  raw frames on cb: $(grep -c HIT $RUN/raw-cb.txt 2>/dev/null) hits"

echo "== check 3: broadcast tunnel B -> A =="
python3 raw_recorder.py ca 11 > $RUN/raw-ca.txt 2>&1 &
python3 console_listen.py 3074 10 XBS-TEST > $RUN/listen-a.json 2>&1 &
LPID=$!
sleep 0.5
python3 console_send.py cb $MAC_CB 0.0.0.1 bcast 3 >/dev/null
wait $LPID
tok=$(tail -1 $RUN/listen-a.json | python3 -c "import json,sys; print(json.load(sys.stdin).get('tag_ok',0))")
if [ "$tok" -ge 3 ]; then ok "console-a received $tok/3 broadcasts through tunnel"; else bad "console-a received $tok/3"; cat $RUN/listen-a.json; fi
echo "  raw frames on ca: $(grep -c HIT $RUN/raw-ca.txt 2>/dev/null) hits"

echo "== check 4: unicast tunnel A -> B (MAC learning) =="
python3 console_listen.py 3074 10 XBS-TEST-ucast > $RUN/listen-uc.json 2>&1 &
LPID=$!
sleep 0.5
python3 raw_recorder.py br-a 5 > $RUN/raw-bra.txt 2>&1 &
ip -o link show ca > $RUN/link-ca.txt 2>&1
python3 console_send.py ca "$MAC_CA" 10.0.2.55 ucast 2 "$MAC_CB" 10.0.2.10 > $RUN/sender-uc.txt 2>&1
echo "sender exit: $?"
wait $LPID
tok=$(tail -1 $RUN/listen-uc.json | python3 -c "import json,sys; print(json.load(sys.stdin).get('tag_ok',0))")
if [ "$tok" = "2" ]; then ok "unicast routed via learned MAC (2/2)"; else bad "unicast failed ($tok/2)"; tail -8 $RUN/log-a.txt; fi
echo "  raw frames on br-a: $(grep -c HIT $RUN/raw-bra.txt 2>/dev/null) hits"

echo "== check 5: loop prevention (idle 6s) =="
before_a=$(python3 -c "import json; print(json.load(open('$RUN/status-a.json'))['stats']['udp_in'])")
before_cap_b=$(python3 -c "import json; print(json.load(open('$RUN/status-b.json'))['stats']['frames_captured'])")
sleep 6
after_a=$(python3 -c "import json; print(json.load(open('$RUN/status-a.json'))['stats']['udp_in'])")
after_cap_b=$(python3 -c "import json; print(json.load(open('$RUN/status-b.json'))['stats']['frames_captured'])")
delta=$(( after_a - before_a + after_cap_b - before_cap_b ))
if [ $delta -le 20 ]; then ok "no tunnel loop (control deltas: $delta in idle)"; else
  bad "possible loop! delta=$delta"
  echo "  ---- diagnostics ----"
  for f in $RUN/log-a.txt $RUN/log-b.txt; do
    echo "  $f: $(wc -l < $f) lines, pings_in=$(grep -c 'ping from' $f), announces=$(grep -c 'announce' $f), helpers=$(grep -c 'cloud helper' $f)"
    tail -15 $f | sed 's/^/    /'
  done
  python3 - <<'EOF'
import json
for side in ('a','b'):
    try:
        s=json.load(open(f"/tmp/xbs-test/status-{side}.json"))
        print(f"  status-{side}: udp_in={s['stats'].get('udp_in')} udp_out={s['stats'].get('udp_out')} nodes={[(n['ip'],n['nickname'],n['ping_ms']) for n in s.get('nodes',[])]}")
    except Exception as e:
        print(f"  status-{side}: ERR {e}")
EOF
fi

echo "== check 6: status files consistent =="
dev_b=$(python3 -c "import json; print(len(json.load(open('$RUN/status-b.json')).get('local_devices',[])))")
node_b=$(python3 -c "import json; print(len(json.load(open('$RUN/status-b.json')).get('nodes',[])))")
if [ "$node_b" = "1" ]; then ok "status shows 1 remote node at B"; else bad "unexpected node count at B: $node_b"; fi
echo "  (local devices seen at B: $dev_b)"

echo
echo "================================"
echo "RESULT: $PASS passed, $FAIL failed"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
