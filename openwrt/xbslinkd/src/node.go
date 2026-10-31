package main

// Node registry & state machine, mirroring xbs_node.cs / xbs_node_list.cs.

import (
        "math/rand"
        "net"
        "sync"
        "time"
)

const (
        minPingDelay       = 20 * time.Second // re-ping when silence longer than this
        maxPingNoResponse  = 30 * time.Second // drop node when no pong for this long
        maxAddNodeTimeout  = 2 * time.Second  // announce confirmation timeout
        maxAskCloudHelper  = 3                // cloud-helper retries per node
        staticPeerInterval = 60 * time.Second // retry interval for static peers (extension)
)

// Node represents a remote XBSlink node.
type Node struct {
        mu sync.Mutex

        announcedIP   net.IP // address the node CLAIMS to be (ip_announced)
        announcedPort uint16
        sendIP        net.IP // address we actually send to (ip_sendfrom)
        sendPort      uint16

        nickname     string
        nicknameRecv bool
        version      string
        versionRecv  bool

        lastPing time.Time
        lastPong time.Time
        delayMS  int32 // last measured RTT, -1 = unknown

        xboxes map[[6]byte]net.IP // MAC -> last seen IP of devices behind this node

        addedTime      time.Time // (re)set when entering the "adding" list
        askCloudHelper int

        rxPkts, txPkts, rxBytes, txBytes uint64
        lastRx                           time.Time
}

func NewNode(announcedIP net.IP, announcedPort uint16, sendIP net.IP, sendPort uint16) *Node {
        n := &Node{
                sendIP:        sendIP,
                sendPort:      sendPort,
                nickname:      "{Anonymous}",
                version:       "(unknown)",
                delayMS:       -1,
                xboxes:        make(map[[6]byte]net.IP),
                addedTime:     time.Now(),
                lastPong:      time.Now(),
                announcedIP:   announcedIP,
                announcedPort: announcedPort,
        }
        if announcedIP == nil {
                n.announcedIP = sendIP
                n.announcedPort = sendPort
        }
        return n
}

func (n *Node) SendAddr() *net.UDPAddr {
        n.mu.Lock()
        defer n.mu.Unlock()
        return &net.UDPAddr{IP: n.sendIP, Port: int(n.sendPort)}
}

// IPPublic mirrors xbs_node.ip_public: the announced address if it is a
// public one, otherwise the observed source address.
func (n *Node) IPPublic() net.IP {
        n.mu.Lock()
        defer n.mu.Unlock()
        if n.announcedIP != nil && !isPrivate(n.announcedIP) && !n.announcedIP.Equal(net.IPv4zero) {
                return n.announcedIP
        }
        return n.sendIP
}

func (n *Node) PortPublic() uint16 {
        n.mu.Lock()
        defer n.mu.Unlock()
        if n.announcedIP != nil && !isPrivate(n.announcedIP) && !n.announcedIP.Equal(net.IPv4zero) {
                return n.announcedPort
        }
        return n.sendPort
}

// Equals mirrors xbs_node.Equals: nodes are the same when announced,
// send-from or public endpoints match.
func (n *Node) Equals(o *Node) bool {
        if n == o {
                return true
        }
        if o == nil {
                return false
        }
        n.mu.Lock()
        defer n.mu.Unlock()
        o.mu.Lock()
        defer o.mu.Unlock()
        if n.announcedIP != nil && o.announcedIP != nil &&
                n.announcedIP.Equal(o.announcedIP) && n.announcedPort == o.announcedPort {
                return true
        }
        if n.sendIP.Equal(o.sendIP) && n.sendPort == o.sendPort {
                return true
        }
        return false
}

func (n *Node) HasXbox(mac [6]byte) bool {
        if isBroadcastMAC(mac) || isZeroMAC(mac) {
                return true
        }
        n.mu.Lock()
        defer n.mu.Unlock()
        _, ok := n.xboxes[mac]
        return ok
}

// AddXbox records a device MAC behind this node. Returns true if new.
func (n *Node) AddXbox(mac [6]byte) bool {
        if isBroadcastMAC(mac) || isZeroMAC(mac) {
                return false
        }
        n.mu.Lock()
        defer n.mu.Unlock()
        if _, ok := n.xboxes[mac]; ok {
                return false
        }
        n.xboxes[mac] = nil
        return true
}

func (n *Node) AddIPToXbox(mac [6]byte, ip net.IP) bool {
        n.mu.Lock()
        defer n.mu.Unlock()
        if _, ok := n.xboxes[mac]; !ok {
                return false
        }
        if ip4 := ip.To4(); ip4 != nil {
                n.xboxes[mac] = ip4
        }
        return true
}

func (n *Node) setNickname(nick string) {
        n.mu.Lock()
        n.nickname = nick
        n.nicknameRecv = true
        n.mu.Unlock()
}
func (n *Node) setVersion(v string) {
        n.mu.Lock()
        n.version = v
        n.versionRecv = true
        n.mu.Unlock()
}
func (n *Node) markPong(delayMS int32) {
        n.mu.Lock()
        n.lastPong = time.Now()
        n.delayMS = delayMS
        n.mu.Unlock()
}
func (n *Node) markPing() {
        n.mu.Lock()
        n.lastPing = time.Now()
        n.mu.Unlock()
}
func (n *Node) markSent(size int) {
        n.mu.Lock()
        n.txPkts++
        n.txBytes += uint64(size)
        n.mu.Unlock()
}
func (n *Node) markRecv(size int) {
        n.mu.Lock()
        n.rxPkts++
        n.rxBytes += uint64(size)
        n.lastRx = time.Now()
        n.mu.Unlock()
}
func (n *Node) sincePong() time.Duration {
        n.mu.Lock()
        defer n.mu.Unlock()
        return time.Since(n.lastPong)
}
func (n *Node) sincePing() time.Duration {
        n.mu.Lock()
        defer n.mu.Unlock()
        return time.Since(n.lastPing)
}
func (n *Node) Nickname() string {
        n.mu.Lock()
        defer n.mu.Unlock()
        return n.nickname
}

func (n *Node) setNicknameIfChanged(nick string) bool {
        n.mu.Lock()
        defer n.mu.Unlock()
        if n.nickname == nick && n.nicknameRecv {
                return false
        }
        n.nickname = nick
        n.nicknameRecv = true
        return true
}

func (n *Node) RxPackets() uint64 { n.mu.Lock(); defer n.mu.Unlock(); return n.rxPkts }
func (n *Node) TxPackets() uint64 { n.mu.Lock(); defer n.mu.Unlock(); return n.txPkts }
func (n *Node) RxBytes() uint64   { n.mu.Lock(); defer n.mu.Unlock(); return n.rxBytes }
func (n *Node) TxBytes() uint64   { n.mu.Lock(); defer n.mu.Unlock(); return n.txBytes }
func (n *Node) Version() string {
        n.mu.Lock()
        defer n.mu.Unlock()
        return n.version
}
func (n *Node) DelayMS() int32 {
        n.mu.Lock()
        defer n.mu.Unlock()
        return n.delayMS
}
func (n *Node) XboxList() map[[6]byte]net.IP {
        n.mu.Lock()
        defer n.mu.Unlock()
        out := make(map[[6]byte]net.IP, len(n.xboxes))
        for k, v := range n.xboxes {
                out[k] = v
        }
        return out
}

func (n *Node) String() string {
        n.mu.Lock()
        defer n.mu.Unlock()
        ip := n.sendIP
        port := n.sendPort
        if n.announcedIP != nil && !isPrivate(n.announcedIP) && !n.announcedIP.Equal(net.IPv4zero) {
                ip, port = n.announcedIP, n.announcedPort
        }
        s := net.JoinHostPort(ip.String(), itoa(port))
        if n.sendPort != port {
                s += "/" + itoa(n.sendPort)
        }
        return s
}

func itoa(u uint16) string {
        if u == 0 {
                return "0"
        }
        var b [5]byte
        i := len(b)
        for u > 0 {
                i--
                b[i] = byte('0' + u%10)
                u /= 10
        }
        return string(b[i:])
}

// ---------------------------------------------------------------- NodeList

type NodeList struct {
        mu    sync.Mutex
        nodes []*Node
        // nodes currently being contacted (waiting for ADDNODE confirmation)
        adding []*Node

        app *App
}

func NewNodeList(app *App) *NodeList {
        return &NodeList{app: app}
}

// LocalNode identity (announced to peers as ip_public:port).
type localIdentity struct {
        IP      net.IP
        Port    uint16
        Nick    string
        Version string
}

func (nl *NodeList) findNode(ip net.IP, port uint16) *Node {
        nl.mu.Lock()
        defer nl.mu.Unlock()
        return nl.findNodeLocked(ip, port)
}

// findNodeLocked is findNode without taking nl.mu (caller must hold it).
func (nl *NodeList) findNodeLocked(ip net.IP, port uint16) *Node {
        for _, n := range nl.nodes {
                n.mu.Lock()
                match := (n.announcedIP != nil && n.announcedIP.Equal(ip) && n.announcedPort == port) ||
                        (n.sendIP.Equal(ip) && n.sendPort == port)
                n.mu.Unlock()
                if match {
                        return n
                }
        }
        return nil
}

func (nl *NodeList) findNodeByAddr(addr *net.UDPAddr) *Node {
        if addr == nil || addr.IP.To4() == nil {
                return nil
        }
        return nl.findNode(addr.IP.To4(), uint16(addr.Port))
}

func (nl *NodeList) findInAdding(ip net.IP, port uint16) bool {
        nl.mu.Lock()
        defer nl.mu.Unlock()
        for _, n := range nl.adding {
                n.mu.Lock()
                match := n.sendIP.Equal(ip) && n.sendPort == port
                n.mu.Unlock()
                if match {
                        return true
                }
        }
        return false
}

// Add registers a fully confirmed node and starts the handshake
// (GETCLIENTVERSION / PING / GETNICKNAME), mirroring xbs_node_list.addNode.
func (nl *NodeList) Add(n *Node) {
        if nl.findNode(n.announcedIP, n.announcedPort) != nil {
                nl.purgeFromAdding(n) // already known; never leave a stale adding entry
                return
        }
        nl.mu.Lock()
        dup := false
        for _, e := range nl.nodes {
                if e.Equals(n) {
                        dup = true
                        break
                }
        }
        if !dup {
                nl.nodes = append(nl.nodes, n)
        }
        nl.mu.Unlock()
        if dup {
                nl.purgeFromAdding(n)
                return
        }
        Log.Infof(" + added node %s", n.String())
        nl.app.sendToNode(n, MsgGetClientVersion, nil, false)
        nl.app.sendPing(n)
        nl.app.sendToNode(n, MsgGetNickname, nil, false)
        nl.purgeFromAdding(n)
        nl.app.statusDirty()
}

func (nl *NodeList) Del(n *Node) {
        nl.mu.Lock()
        removed := false
        for i, e := range nl.nodes {
                if e.Equals(n) {
                        nl.nodes = append(nl.nodes[:i], nl.nodes[i+1:]...)
                        removed = true
                        Log.Infof(" - removed node %s", e.String())
                        break
                }
        }
        nl.mu.Unlock()
        if removed {
                nl.app.statusDirty()
        }
}

func (nl *NodeList) DelByAddr(ip net.IP, port uint16) *Node {
        n := nl.findNode(ip, port)
        if n != nil {
                nl.Del(n)
        }
        return n
}

func (nl *NodeList) Count() int {
        nl.mu.Lock()
        defer nl.mu.Unlock()
        return len(nl.nodes)
}

// All returns a snapshot of the current node list.
func (nl *NodeList) All() []*Node {
        nl.mu.Lock()
        defer nl.mu.Unlock()
        out := make([]*Node, len(nl.nodes))
        copy(out, nl.nodes)
        return out
}

func (nl *NodeList) Clear() {
        nl.mu.Lock()
        nl.nodes = nil
        nl.mu.Unlock()
        nl.app.statusDirty()
}

// SendNodeListToNode mirrors sendNodeListToNode: KNOWNNODE messages about all
// local nodes (except the receiver itself).
func (nl *NodeList) SendNodeListToNode(target *Node) {
        for _, n := range nl.All() {
                if !n.Equals(target) {
                        nl.app.sendToNode(target, MsgKnownNode, MarshalIPPort(n.IPPublic(), n.PortPublic()), false)
                }
        }
}

// TryAddingNode mirrors tryAddingNode: remember the candidate and ANNOUNCE to
// it (with the cloud name attached when we are part of a cloud).
func (nl *NodeList) TryAddingNode(ip net.IP, port uint16) {
        if ip == nil || ip.To4() == nil || port == 0 {
                return
        }
        ip4 := ip.To4()
        if nl.findNode(ip4, port) != nil {
                return
        }
        if nl.findInAdding(ip4, port) {
                return
        }
        n := NewNode(nil, 0, ip4, port)
        nl.mu.Lock()
        nl.adding = append(nl.adding, n)
        nl.mu.Unlock()

        opts := make(map[string]string)
        if cn := nl.app.currentCloudName(); cn != "" {
                opts[OptionCloudName] = cn
        }
        nl.app.sendToNode(n, MsgAnnounce, []byte(EncodeOptions(opts)), false)
        Log.Debugf("announce sent to %s", n.String())
}

func (nl *NodeList) purgeFromAdding(n *Node) {
        nl.mu.Lock()
        for i, e := range nl.adding {
                if e.Equals(n) {
                        nl.adding = append(nl.adding[:i], nl.adding[i+1:]...)
                        break
                }
        }
        nl.mu.Unlock()
}

// checkAddingList mirrors checkNodesInAddingList: after 2s without
// confirmation, ask a random known node to play cloud-helper, max 3 times.
func (nl *NodeList) checkAddingList() {
        nl.mu.Lock()
        var helpers []*Node
        var drop []*Node
        for _, n := range nl.adding {
                if nl.findNodeLocked(n.sendIP, n.sendPort) != nil ||
                        nl.findNodeLocked(n.announcedIP, n.announcedPort) != nil {
                        drop = append(drop, n) // confirmed in the meantime: stale entry
                        continue
                }
                if time.Since(n.addedTime) >= maxAddNodeTimeout {
                        if n.askCloudHelper < maxAskCloudHelper {
                                helpers = append(helpers, n)
                                n.askCloudHelper++
                                n.addedTime = time.Now()
                        } else {
                                drop = append(drop, n)
                        }
                }
        }
        for _, n := range drop {
                for i, e := range nl.adding {
                        if e == n {
                                nl.adding = append(nl.adding[:i], nl.adding[i+1:]...)
                                break
                        }
                }
        }
        nl.mu.Unlock()

        if len(helpers) == 0 {
                return
        }
        all := nl.All()
        if len(all) == 0 {
                return
        }
        for _, n := range helpers {
                peer := all[randIntn(len(all))]
                nl.app.sendToNode(peer, MsgToCloudHelper, MarshalIPPort(n.IPPublic(), n.PortPublic()), false)
                Log.Debugf("asked cloud helper %s to help reach %s", peer.String(), n.String())
        }
}

// PingLoop mirrors ping_nodes_thread (1s tick) + static peer retries.
func (nl *NodeList) PingLoop(stop <-chan struct{}) {
        tick := time.NewTicker(time.Second)
        defer tick.Stop()
        lastStaticTry := time.Now().Add(staticPeerInterval) // first try right away handled by Start()
        first := true
        for {
                select {
                case <-stop:
                        return
                case <-tick.C:
                }
                if first {
                        first = false
                        nl.retryStaticPeers()
                        lastStaticTry = time.Now()
                }
                for _, n := range nl.All() {
                        sp, sl := n.sincePing(), n.sincePong()
                        if sp > minPingDelay || sl > minPingDelay {
                                nl.app.sendPing(n)
                        }
                        if n.sincePong() > maxPingNoResponse {
                                Log.Infof(" - node %s timed out (no pong for %ds)", n.String(), int(n.sincePong().Seconds()))
                                nl.app.sendToNode(n, MsgDelNode, MarshalIPPort(nl.app.localIP(), nl.app.localPort()), false)
                                nl.Del(n)
                                continue
                        }
                        // every even second: request missing node details
                        if time.Now().Second()%2 == 0 {
                                if !n.versionRecv {
                                        nl.app.sendToNode(n, MsgGetClientVersion, nil, false)
                                }
                                if !n.nicknameRecv {
                                        nl.app.sendToNode(n, MsgGetNickname, nil, false)
                                }
                                if n.DelayMS() < 0 {
                                        nl.app.sendPing(n)
                                }
                        }
                }
                nl.checkAddingList()
                if time.Since(lastStaticTry) >= staticPeerInterval {
                        nl.retryStaticPeers()
                        lastStaticTry = time.Now()
                }
        }
}

func (nl *NodeList) retryStaticPeers() {
        for _, p := range nl.app.cfg.Peers {
                ip, portStr, err := net.SplitHostPort(p)
                if err != nil {
                        continue
                }
                ipAddr := net.ParseIP(ip)
                if ipAddr == nil {
                        // resolve hostname once per attempt
                        if addrs, err := net.LookupHost(ip); err == nil && len(addrs) > 0 {
                                ipAddr = net.ParseIP(addrs[0])
                        }
                }
                if ipAddr == nil {
                        continue
                }
                port := uint16(0)
                for _, c := range portStr {
                        port = port*10 + uint16(c-'0')
                }
                if port == 0 {
                        port = uint16(nl.app.cfg.Port)
                }
                nl.TryAddingNode(ipAddr, port)
        }
}

// DistributeFrame mirrors distributeDataPacket: broadcast frames go to all
// nodes, unicast frames only to the node that owns the destination MAC.
func (nl *NodeList) DistributeFrame(dstMAC [6]byte, frame []byte) (sent int) {
        for _, n := range nl.All() {
                if n.HasXbox(dstMAC) {
                        nl.app.sendFrame(n, frame)
                        sent++
                        if !isBroadcastMAC(dstMAC) && !isZeroMAC(dstMAC) {
                                return sent // unicast: first (and only) match wins
                        }
                }
        }
        return sent
}

func isBroadcastMAC(m [6]byte) bool {
        return m == [6]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
}
func isZeroMAC(m [6]byte) bool { return m == [6]byte{} }

func isPrivate(ip net.IP) bool {
        if ip4 := ip.To4(); ip4 != nil {
                return ip4[0] == 10 ||
                        (ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
                        (ip4[0] == 192 && ip4[1] == 168) ||
                        ip4[0] == 127 ||
                        (ip4[0] == 169 && ip4[1] == 254) ||
                        (ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127)
        }
        return false
}

func randIntn(n int) int {
        if n <= 0 {
                return 0
        }
        return rand.Intn(n)
}
