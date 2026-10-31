package main

// App glues transport, capture, node list, cloud and status together and
// implements the message dispatch table (xbs_udp_listener.dispatch_in_msg).

import (
        "net"
        "sync"
        "sync/atomic"
        "time"
)

type App struct {
        cfg       *Config
        nodes     *NodeList
        transport *Transport
        capture   *Capture
        cloud     *Cloud
        status    *Status

        localIPValue atomic.Pointer[net.IP] // announced local IP
        localPortV   uint16

        portReachable atomic.Bool
        helloSignal   chan struct{}
        helloMu       sync.Mutex

        controlQ chan ctrlMsg

        stop    chan struct{}
        stopped chan struct{}
        wg      sync.WaitGroup
}

func NewApp(cfg *Config) (*App, error) {
        stats.Init()
        app := &App{
                cfg:      cfg,
                controlQ: make(chan ctrlMsg, 256),
                stop:     make(chan struct{}),
                stopped:  make(chan struct{}),
        }
        app.nodes = NewNodeList(app)

        tr, err := NewTransport(app)
        if err != nil {
                return nil, err
        }
        app.transport = tr
        app.localPortV = uint16(tr.Port())

        app.localIPValue.Store(detectLocalIP(cfg.NodeIP, cfg.Interface))
        Log.Infof(" * local node identity: %s:%d (nickname %q)", app.localIP(), app.localPort(), cfg.Nickname)

        cap, err := NewCapture(app)
        if err != nil {
                tr.Stop()
                return nil, err
        }
        app.capture = cap
        if len(cap.gatewayIPs) > 0 {
                Log.Infof(" * gateway IPs excluded: %v", cap.gatewayIPs)
        }

        if cfg.StatusPort > 0 || cfg.StatusFile != "" {
                app.status = NewStatus(app)
        }
        if cfg.CloudName != "" {
                app.cloud = NewCloud(app)
        }
        return app, nil
}

func (a *App) Start() error {
        stop := a.stop
        a.transport.Start(stop)
        a.capture.Start(stop, &a.wg)

        a.wg.Add(1)
        go func() {
                defer a.wg.Done()
                a.nodes.PingLoop(stop)
        }()

        a.wg.Add(1)
        go func() {
                defer a.wg.Done()
                for {
                        select {
                        case <-stop:
                                return
                        case m := <-a.controlQ:
                                a.handleControl(m.from, m.typ, m.data)
                        }
                }
        }()

        if a.status != nil {
                a.status.Start(stop, &a.wg)
        }
        if a.cloud != nil {
                a.cloud.Start(stop, &a.wg)
        }
        Log.Infof(" * capturing on interface %s (mac %s)", a.capture.ifName, macString(a.capture.ifMAC))
        if len(a.cfg.Peers) > 0 {
                Log.Infof(" * static peers: %v", a.cfg.Peers)
        }
        if a.cloud != nil {
                Log.Infof(" * cloud enabled: %q @ %s", a.cfg.CloudName, a.cfg.CloudServer)
        }
        return nil
}

func (a *App) Stop() {
        // say goodbye
        a.sendLogoff()
        if a.cloud != nil {
                a.cloud.Leave() // best effort
        }
        close(a.stop)
        // Close the capture sockets BEFORE waiting: the RX loops block inside
        // recvfrom(2) on an idle LAN and only wake up when the fd is closed.
        a.capture.Close()
        a.wg.Wait()
        a.transport.Stop()
        close(a.stopped)
}

// ---------------------------------------------------------------- identity

func (a *App) localIP() net.IP   { return *a.localIPValue.Load() }
func (a *App) localPort() uint16 { return a.localPortV }

// detectLocalIP picks the announced IP, mirroring the original (which uses
// the capture device's address for local_node): explicit config wins, then
// the capture interface's IPv4, then the first public IPv4, then the first
// private IPv4 (the cloud server substitutes private addresses with the HTTP
// source address anyway).
func detectLocalIP(cfgNodeIP, cfgInterface string) *net.IP {
        if cfgNodeIP != "" {
                if ip := net.ParseIP(cfgNodeIP); ip != nil {
                        p := ip.To4()
                        return &p
                }
                Log.Warnf("bad node-ip %q, auto-detecting", cfgNodeIP)
        }
        if cfgInterface != "" {
                if ifi, err := net.InterfaceByName(cfgInterface); err == nil {
                        addrs, err := ifi.Addrs()
                        if err == nil {
                                for _, ad := range addrs {
                                        if ipn, ok := ad.(*net.IPNet); ok {
                                                if ip4 := ipn.IP.To4(); ip4 != nil && ip4[0] != 127 {
                                                        return anyIPv4(ip4)
                                                }
                                        }
                                }
                        }
                }
        }
        ifaces, err := net.Interfaces()
        if err != nil {
                return anyIPv4(net.IPv4zero)
        }
        var private net.IP
        for _, ifi := range ifaces {
                if ifi.Flags&net.FlagLoopback != 0 || ifi.Flags&net.FlagUp == 0 {
                        continue
                }
                addrs, err := ifi.Addrs()
                if err != nil {
                        continue
                }
                for _, ad := range addrs {
                        if ipn, ok := ad.(*net.IPNet); ok {
                                if ip4 := ipn.IP.To4(); ip4 != nil {
                                        if !isPrivate(ip4) {
                                                return anyIPv4(ip4)
                                        }
                                        if private == nil && ip4[0] != 127 {
                                                private = ip4
                                        }
                                }
                        }
                }
        }
        if private != nil {
                return anyIPv4(private)
        }
        return anyIPv4(net.IPv4zero)
}

func anyIPv4(ip net.IP) *net.IP {
        if ip4 := ip.To4(); ip4 != nil {
                return &ip4
        }
        return nil
}

func (a *App) currentCloudName() string {
        if a.cloud == nil {
                return ""
        }
        return a.cloud.JoinedName()
}

// ---------------------------------------------------------------- sending

func (a *App) sendToNode(n *Node, t MsgType, data []byte, highPrio bool) {
        a.transport.SendTo(n.SendAddr(), t, data, highPrio)
        n.markSent(len(data) + msgHeaderLen)
}

func (a *App) sendFrame(n *Node, frame []byte) {
        a.transport.SendTo(n.SendAddr(), MsgData, frame, true)
        n.markSent(len(frame) + msgHeaderLen)
}

func (a *App) sendPing(n *Node) {
        payload := NETTicks(time.Now())
        a.recordPingSent(payload)
        a.sendToNode(n, MsgPing, payload, false)
        n.markPing()
}

func (a *App) sendLogoff() {
        for _, n := range a.nodes.All() {
                a.sendToNode(n, MsgDelNode, MarshalIPPort(a.localIP(), a.localPort()), false)
        }
}

func (a *App) distributeFrame(dstMAC [6]byte, frame []byte) {
        sent := a.nodes.DistributeFrame(dstMAC, frame)
        if sent == 0 {
                stats.addNoRoute()
        }
}

// ---------------------------------------------------------------- receive

type ctrlMsg struct {
        from *net.UDPAddr
        typ  MsgType
        data []byte
}

// enqueueControl queues a control message for the single dispatcher
// goroutine (mirrors the original's dispatcher queue; DATA bypasses it).
func (a *App) enqueueControl(from *net.UDPAddr, typ MsgType, data []byte) {
        m := ctrlMsg{from: from, typ: typ, data: data}
        select {
        case a.controlQ <- m:
        default:
                Log.Warnf("control queue full, dropping %v from %s", typ, from)
        }
}

// goroutines spawned in enqueueControl.

func (a *App) handleControl(from *net.UDPAddr, typ MsgType, data []byte) {
        sendingNode := a.nodes.findNodeByAddr(from)
        switch typ {
        case MsgAnnounce:
                opts := ParseOptions(string(data))
                if cloud, ok := opts[OptionCloudName]; ok {
                        if cloud != a.currentCloudName() {
                                Log.Debugf("announce from %s for foreign cloud %q ignored", from, cloud)
                                return
                        }
                }
                peer := NewNode(nil, 0, from.IP.To4(), uint16(from.Port))
                peer.markRecv(len(data) + msgHeaderLen)
                a.sendToNode(peer, MsgAddNode, MarshalIPPort(a.localIP(), a.localPort()), false)
                Log.Debugf("announce from %s -> sent ADDNODE", from)

        case MsgAddNode:
                ip, port, ok := ParseIPPort(data)
                if !ok {
                        return
                }
                if sendingNode == nil {
                        n := NewNode(ip, port, from.IP.To4(), uint16(from.Port))
                        n.markRecv(len(data) + msgHeaderLen)
                        a.nodes.Add(n)
                        a.sendToNode(n, MsgAddNode, MarshalIPPort(a.localIP(), a.localPort()), false)
                        if a.currentCloudName() == "" {
                                a.nodes.SendNodeListToNode(n)
                        }
                        Log.Infof("handshake with new node %s (announced %s)", from, net.JoinHostPort(ip.String(), itoa(port)))
                }

        case MsgKnownNode:
                ip, port, ok := ParseIPPort(data)
                if !ok {
                        return
                }
                // only accept KNOWNNODE when not part of a cloud (original behavior)
                if a.currentCloudName() == "" && a.nodes.findNode(ip, port) == nil {
                        a.nodes.TryAddingNode(ip, port)
                }

        case MsgDelNode:
                if n := a.nodes.DelByAddr(from.IP.To4(), uint16(from.Port)); n != nil {
                        Log.Infof("node %s left", n.String())
                }

        case MsgPing:
                a.transport.SendTo(from, MsgPong, data, false)
                Log.Debugf("ping from %s -> pong", from)

        case MsgPong:
                if sendingNode != nil {
                        delayMS := int32(-1)
                        if len(data) >= 8 {
                                // we compute RTT locally from the ping map
                                delayMS = a.takePingRTT(data)
                        }
                        sendingNode.markPong(delayMS)
                        a.statusDirty()
                }

        case MsgGetNodeList:
                target := sendingNode
                if target == nil {
                        target = NewNode(nil, 0, from.IP.To4(), uint16(from.Port))
                }
                a.nodes.SendNodeListToNode(target)

        case MsgGetClientVersion:
                a.transport.SendTo(from, MsgClientVersion, MarshalVersion(a.cfg.ClientVersion), false)

        case MsgClientVersion:
                if sendingNode != nil {
                        sendingNode.setVersion(ParseVersion(data))
                        a.statusDirty()
                }

        case MsgGetNickname:
                a.transport.SendTo(from, MsgNickname, []byte(a.cfg.Nickname), false)

        case MsgNickname:
                if sendingNode != nil && sendingNode.setNicknameIfChanged(string(data)) {
                        Log.Infof("node %s is known as %q", sendingNode.String(), string(data))
                        a.statusDirty()
                }

        case MsgChatMsg:
                if sendingNode != nil {
                        Log.Infof("[chat] %s: %s", sendingNode.Nickname(), string(data))
                }

        case MsgServerHello:
                // port reachability probe from the cloud server
                a.portReachable.Store(true)
                a.helloMu.Lock()
                if a.helloSignal != nil {
                        select {
                        case a.helloSignal <- struct{}{}:
                        default:
                        }
                }
                a.helloMu.Unlock()
                Log.Infof(" * SERVERHELLO received: node port is reachable from the internet")

        case MsgToCloudHelper:
                ip, port, ok := ParseIPPort(data)
                if !ok {
                        return
                }
                nodeFrom := sendingNode
                nodeTo := a.nodes.findNode(ip, port)
                if nodeFrom != nil && nodeTo != nil {
                        a.sendToNode(nodeTo, MsgFromCloudHelper, MarshalIPPort(nodeFrom.IPPublic(), nodeFrom.PortPublic()), false)
                }

        case MsgFromCloudHelper:
                ip, port, ok := ParseIPPort(data)
                if !ok {
                        return
                }
                a.nodes.TryAddingNode(ip, port)
        }
}

// Ping RTT bookkeeping: payload of our PINGs -> time sent.
var pingMap = struct {
        sync.Mutex
        m map[[8]byte]time.Time
}{m: make(map[[8]byte]time.Time)}

func (a *App) recordPingSent(payload []byte) {
        if len(payload) != 8 {
                return
        }
        pingMap.Lock()
        if len(pingMap.m) > 256 {
                pingMap.m = make(map[[8]byte]time.Time)
        }
        var key [8]byte
        copy(key[:], payload)
        pingMap.m[key] = time.Now()
        pingMap.Unlock()
}

func (a *App) takePingRTT(payload []byte) int32 {
        if len(payload) != 8 {
                return -1
        }
        var key [8]byte
        copy(key[:], payload)
        pingMap.Lock()
        defer pingMap.Unlock()
        if t, ok := pingMap.m[key]; ok {
                delete(pingMap.m, key)
                return int32(time.Since(t).Milliseconds())
        }
        return -1
}

func (a *App) statusDirty() {
        if a.status != nil {
                a.status.MarkDirty()
        }
}

// handleData is the DATA fast path: inject the remote frame into the LAN.
func (a *App) handleData(from *net.UDPAddr, frame []byte) {
        sendingNode := a.nodes.findNodeByAddr(from)
        if sendingNode != nil {
                sendingNode.markRecv(len(frame) + msgHeaderLen)
        }
        a.capture.Inject(frame, sendingNode)
}

// armHelloProbe must be called BEFORE the SENDHELLO HTTP request is issued:
// the server answers with a UDP SERVERHELLO that may arrive before the HTTP
// response, and the notification must not be lost (matches the original's
// _locker_HELLO handling).
func (a *App) armHelloProbe() {
        a.helloMu.Lock()
        a.helloSignal = make(chan struct{}, 1)
        a.helloMu.Unlock()
}

func (a *App) waitHelloSignal(timeout time.Duration) bool {
        a.helloMu.Lock()
        ch := a.helloSignal
        a.helloMu.Unlock()
        if ch == nil {
                return false
        }
        select {
        case <-ch:
                a.helloMu.Lock()
                a.helloSignal = nil
                a.helloMu.Unlock()
                return true
        case <-time.After(timeout):
                a.helloMu.Lock()
                a.helloSignal = nil
                a.helloMu.Unlock()
                return false
        }
}
