package main

// LAN packet capture & injection via AF_PACKET, replacing the original's
// libpcap (SharpPcap) dependency. Pure Go, no cgo: cross-compiles to any
// OpenWrt target (mips/mipsle/arm/x86).

import (
        "fmt"
        "net"
        "os"
        "sync"
        "time"

        "golang.org/x/sys/unix"
)

// Capture sniffs game console traffic on the LAN and injects remote frames.
//
// On a LAN bridge (br-lan on OpenWrt) a packet socket bound to the master
// device only sees frames that travel UP into the host stack - port-to-port
// unicast frames are forwarded by the bridge without ever reaching the
// master tap. We therefore open one RX socket per BRIDGE PORT (each port tap
// sees every frame ingressing through it) and keep injection on the master,
// where the bridge floods the frame out to all ports.
type Capture struct {
        app     *App
        ifIndex int
        ifName  string
        ifMAC   [6]byte

        rxTargets []rxTarget // one AF_PACKET socket per capture device
        txfd      int

        // MACs whose frames were injected FROM remote nodes -> drop on sniff
        injectedMACs map[[6]byte]int64 // mac -> expiry unix ms
        injectedMu   sync.Mutex

        // MACs seen sniffing on the LAN (local devices), for status page
        sniffedMACs map[[6]byte]net.IP
        sniffedMu   sync.Mutex

        gatewayIPs []net.IP
}

type rxTarget struct {
        name    string
        ifIndex int
        fd      int
}

func htons(i uint16) uint16 { return i<<8 | i>>8 }

func NewCapture(app *App) (*Capture, error) {
        ifi, err := net.InterfaceByName(app.cfg.Interface)
        if err != nil {
                return nil, err
        }
        var mac [6]byte
        copy(mac[:], ifi.HardwareAddr)

        c := &Capture{
                app:          app,
                ifIndex:      ifi.Index,
                ifName:       ifi.Name,
                ifMAC:        mac,
                injectedMACs: make(map[[6]byte]int64),
                sniffedMACs:  make(map[[6]byte]net.IP),
                gatewayIPs:   app.cfg.GatewayIPs,
        }

        if len(c.gatewayIPs) == 0 {
                c.gatewayIPs = detectGateways(ifi.Name)
        }

        // RX sockets: one per bridge port when the interface is a bridge,
        // the interface itself otherwise
        targets := captureTargets(ifi.Name, ifi.Index)
        for _, tname := range targets {
                ifi2, err := net.InterfaceByName(tname)
                if err != nil {
                        continue
                }
                fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ALL)))
                if err != nil {
                        return nil, err
                }
                rx := &unix.SockaddrLinklayer{Ifindex: ifi2.Index, Protocol: htons(unix.ETH_P_ALL)}
                if err := unix.Bind(fd, rx); err != nil {
                        return nil, err
                }
                // attach the static kernel filter: ether broadcast OR (ip && udp)
                if err := attachBPF(fd, buildFilter()); err != nil {
                        return nil, err
                }
                c.rxTargets = append(c.rxTargets, rxTarget{name: tname, ifIndex: ifi2.Index, fd: fd})
        }
        if len(c.rxTargets) == 0 {
                return nil, fmt.Errorf("no usable capture target on %s", ifi.Name)
        }

        // TX socket (on the configured interface: bridge master floods to ports)
        c.txfd, err = unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, 0)
        if err != nil {
                return nil, err
        }
        tx := &unix.SockaddrLinklayer{Ifindex: ifi.Index, Protocol: 0x0003}
        if err := unix.Bind(c.txfd, tx); err != nil {
                return nil, err
        }

        return c, nil
}

// captureTargets returns the list of devices to sniff on: all bridge ports
// when iface is a bridge (via rtnetlink), otherwise just iface.
func captureTargets(iface string, ifindex int) []string {
        ports := bridgePorts(ifindex)
        if len(ports) == 0 {
                return []string{iface}
        }
        Log.Infof(" * bridge detected: capturing on %d port(s): %v", len(ports), ports)
        return ports
}

// Gateways returns the configured / detected gateway IPs.
func (c *Capture) Gateways() []net.IP { return c.gatewayIPs }

func (c *Capture) IfMAC() [6]byte { return c.ifMAC }

// Start launches one capture read loop per RX target.
func (c *Capture) Start(stop <-chan struct{}, wg *sync.WaitGroup) {
        for _, t := range c.rxTargets {
                t := t
                wg.Add(1)
                go func() {
                        defer wg.Done()
                        c.readLoop(stop, t)
                }()
        }
}

func (c *Capture) readLoop(stop <-chan struct{}, t rxTarget) {
        buf := make([]byte, 65535)
        for {
                n, err := unix.Read(t.fd, buf)
                select {
                case <-stop:
                        return
                default:
                }
                if err != nil {
                        select {
                        case <-stop:
                                return // fd closed for shutdown
                        default:
                        }
                        if err == unix.EINTR {
                                continue
                        }
                        Log.Errorf("capture read (%s): %v", t.name, err)
                        time.Sleep(100 * time.Millisecond)
                        continue
                }
                if n < 14 {
                        continue
                }
                frame := make([]byte, n)
                copy(frame, buf[:n])
                c.handleCapturedFrame(frame)
        }
}

func (c *Capture) handleCapturedFrame(frame []byte) {
        var srcMAC, dstMAC [6]byte
        copy(srcMAC[:], frame[6:12])
        dstMACBytes := frame[0:6]
        copy(dstMAC[:], dstMACBytes)

        var srcIP net.IP
        if _, _, sip, _, _, _, ok := parseL3(frame); ok {
                srcIP = sip
        }
        c.sniffedMu.Lock()
        c.sniffedMACs[srcMAC] = srcIP
        c.sniffedMu.Unlock()

        stats.addCaptured()
        if !c.acceptCapture(srcMAC, dstMAC, frame) {
                return
        }
        Log.Debugf("s> %s => %s (%d bytes)", macString(srcMAC), macString(dstMAC), len(frame))
        c.app.distributeFrame(dstMAC, frame)
}

// acceptCapture mirrors the original pcap include/exclude filter logic.
func (c *Capture) acceptCapture(srcMAC, dstMAC [6]byte, frame []byte) bool {
        if srcMAC == c.ifMAC {
                return false // our own interface
        }
        c.injectedMu.Lock()
        exp, isInjected := c.injectedMACs[srcMAC]
        c.injectedMu.Unlock()
        if isInjected && exp > time.Now().UnixMilli() {
                return false // loop prevention: we injected this frame earlier
        }

        // user-specified special MACs: forward everything from them
        if len(c.app.cfg.SpecialMACs) > 0 {
                for _, m := range c.app.cfg.SpecialMACs {
                        if m == srcMAC {
                                return true
                        }
                }
                return false // only_forward_special_macs mode
        }

        ip4, proto, srcIP, dstIP, srcPort, dstPort, ok := parseL3(frame)
        if !ok {
                return false
        }
        _ = ip4

        // exclude gateway IPs
        for _, g := range c.gatewayIPs {
                if srcIP.Equal(g) || dstIP.Equal(g) {
                        return false
                }
        }

        // exclude well known ports (src or dst <1024)
        if !c.app.cfg.AllowWellKnownPorts && (proto == protoUDP || proto == protoTCP) {
                if srcPort < 1024 || dstPort < 1024 {
                        return false
                }
        }

        // game console signature
        if srcIP.Equal(net.IPv4(0, 0, 0, 1)) || dstIP.Equal(net.IPv4(0, 0, 0, 1)) {
                return true
        }
        if proto == protoUDP {
                if dstPort >= 3074 && dstPort <= 3075 {
                        return true
                }
                if dstPort >= 14000 && dstPort <= 14001 {
                        return true
                }
                // optionally forward all high port broadcasts
                if c.app.cfg.ForwardHighPortBroadcasts && isBroadcastMAC(dstMAC) && dstPort >= 1024 {
                        return true
                }
        }
        return false
}

// Inject sends a remote Ethernet frame into the local LAN, mirroring
// injectRemotePacket (record BEFORE send for loop prevention).
func (c *Capture) Inject(frame []byte, fromNode *Node) {
        if len(frame) < 14 {
                return
        }
        var srcMAC [6]byte
        copy(srcMAC[:], frame[6:12])

        // record injected MAC first (original does the same)
        c.injectedMu.Lock()
        c.injectedMACs[srcMAC] = time.Now().UnixMilli() + int64(c.app.cfg.DeDupeWindow)
        // occasional cleanup
        if len(c.injectedMACs) > 512 {
                now := time.Now().UnixMilli()
                for k, exp := range c.injectedMACs {
                        if exp < now {
                                delete(c.injectedMACs, k)
                        }
                }
        }
        c.injectedMu.Unlock()

        // register the device with the sending node (unicast return path)
        if fromNode != nil {
                if fromNode.AddXbox(srcMAC) {
                        Log.Infof(" ~ device %s discovered at node %s", macString(srcMAC), fromNode.String())
                        c.app.statusDirty()
                }
        }

        _, proto, srcIP, dstIP, _, dstPort, _ := parseL3(frame)

        // filter rules from is_injected_packet_to_be_filtered
        for _, g := range c.gatewayIPs {
                if srcIP.Equal(g) || dstIP.Equal(g) {
                        return
                }
        }
        if !c.app.cfg.AllowWellKnownPorts && dstPort < 1024 && (proto == protoUDP || proto == protoTCP) {
                return
        }

        stats.addInjected()
        Log.Debugf("i> %s (%d bytes)", macString(srcMAC), len(frame))
        if err := c.sendFrame(frame); err != nil {
                Log.Errorf("inject failed: %v", err)
        }
}

func (c *Capture) sendFrame(frame []byte) error {
        addr := &unix.SockaddrLinklayer{
                Ifindex:  c.ifIndex,
                Protocol: 0x0003,
                Halen:    6,
        }
        return unix.Sendto(c.txfd, frame, 0, addr)
}

func (c *Capture) Close() {
        for _, t := range c.rxTargets {
                unix.Close(t.fd)
        }
        unix.Close(c.txfd)
}

// SniffedMACsMap returns recently seen local devices (MAC -> last IP).
func (c *Capture) SniffedMACsMap() map[[6]byte]net.IP {
        c.sniffedMu.Lock()
        defer c.sniffedMu.Unlock()
        out := make(map[[6]byte]net.IP, len(c.sniffedMACs))
        for m, ip := range c.sniffedMACs {
                out[m] = ip
        }
        return out
}

// ---------------------------------------------------------------- helpers

const (
        protoICMP = 1
        protoTCP  = 6
        protoUDP  = 17
)

// parseL3 extracts IPv4/ARP info from an Ethernet II frame.
func parseL3(frame []byte) (ip4 []byte, proto byte, srcIP, dstIP net.IP, srcPort, dstPort uint16, ok bool) {
        if len(frame) < 14 {
                return nil, 0, nil, nil, 0, 0, false
        }
        ethertype := uint16(frame[12])<<8 | uint16(frame[13])
        switch ethertype {
        case 0x0806: // ARP
                if len(frame) < 14+28 {
                        return nil, 0, nil, nil, 0, 0, false
                }
                // ARP over IPv4: sender IP at 28..32, target IP at 38..42 (frame offset)
                return frame[14 : 14+20], 0, net.IP(frame[14+14 : 14+18]), net.IP(frame[14+24 : 14+28]), 0, 0, true
        case 0x0800: // IPv4
                if len(frame) < 14+20 {
                        return nil, 0, nil, nil, 0, 0, false
                }
                h := frame[14]
                if h>>4 != 4 {
                        return nil, 0, nil, nil, 0, 0, false
                }
                ihl := int(h&0x0f) * 4
                if ihl < 20 || len(frame) < 14+ihl {
                        return nil, 0, nil, nil, 0, 0, false
                }
                frag := (uint16(frame[14+6])<<8 | uint16(frame[14+7])) & 0x1fff
                if frag != 0 {
                        return nil, 0, nil, nil, 0, 0, false // non-first fragment: no ports
                }
                proto = frame[14+9]
                srcIP = net.IP(frame[14+12 : 14+16])
                dstIP = net.IP(frame[14+16 : 14+20])
                if proto == protoUDP || proto == protoTCP {
                        if len(frame) >= 14+ihl+4 {
                                srcPort = uint16(frame[14+ihl])<<8 | uint16(frame[14+ihl+1])
                                dstPort = uint16(frame[14+ihl+2])<<8 | uint16(frame[14+ihl+3])
                        }
                }
                ip4 = frame[14 : 14+ihl]
                return ip4, proto, srcIP, dstIP, srcPort, dstPort, true
        }
        return nil, 0, nil, nil, 0, 0, false
}

// detectGateways parses /proc/net/route for the default gateway of ifname.
func detectGateways(ifname string) []net.IP {
        var out []net.IP
        lines, err := readLines("/proc/net/route")
        if err != nil {
                return nil
        }
        for _, ln := range lines {
                f := splitFields(ln)
                if len(f) < 4 || f[0] != ifname {
                        continue
                }
                if f[1] == "00000000" && f[2] != "00000000" { // default route
                        if ip := parseHexLEIP(f[2]); ip != nil {
                                out = append(out, ip)
                        }
                }
        }
        return out
}

func parseHexLEIP(s string) net.IP {
        if len(s) != 8 {
                return nil
        }
        b := make(net.IP, 4)
        for i := 0; i < 4; i++ {
                v, ok := hexVal(s[i*2]), hexVal(s[i*2+1])
                if v < 0 || ok < 0 {
                        return nil
                }
                b[3-i] = byte(v<<4 | ok)
        }
        return b
}

func hexVal(c byte) int {
        switch {
        case c >= '0' && c <= '9':
                return int(c - '0')
        case c >= 'a' && c <= 'f':
                return int(c-'a') + 10
        case c >= 'A' && c <= 'F':
                return int(c-'A') + 10
        }
        return -1
}

func readLines(path string) ([]string, error) {
        data, err := os.ReadFile(path)
        if err != nil {
                return nil, err
        }
        var out []string
        start := 0
        for i := 0; i <= len(data); i++ {
                if i == len(data) || data[i] == '\n' {
                        if i > start {
                                out = append(out, string(data[start:i]))
                        }
                        start = i + 1
                }
        }
        return out, nil
}

func splitFields(s string) []string {
        var out []string
        start := -1
        for i := 0; i < len(s); i++ {
                if s[i] != ' ' && s[i] != '\t' {
                        if start < 0 {
                                start = i
                        }
                } else if start >= 0 {
                        out = append(out, s[start:i])
                        start = -1
                }
        }
        if start >= 0 {
                out = append(out, s[start:])
        }
        return out
}

// ---------------------------------------------------------------- BPF

type bpfIns struct {
        code uint16
        jt   byte
        jf   byte
        k    uint32
}

// buildFilter: accept (ether broadcast) OR (ip && proto==udp).
// Exact include/exclude rules are applied in userspace (they are dynamic).
func buildFilter() []bpfIns {
        const (
                ldhAbs = 0x28 // BPF_LD|BPF_H|BPF_ABS
                ldbAbs = 0x30 // BPF_LD|BPF_B|BPF_ABS
                jeq    = 0x15 // BPF_JMP|BPF_JEQ|BPF_K
                ret    = 0x06 // BPF_RET|BPF_K
                accept = 65535
        )
        return []bpfIns{
                // broadcast: first 6 bytes must be all 0xff
                {ldhAbs, 0, 0, 0},
                {jeq, 0, 5, 0xffff},
                {ldhAbs, 0, 0, 2},
                {jeq, 0, 3, 0xffff},
                {ldhAbs, 0, 0, 4},
                {jeq, 0, 1, 0xffff},
                // ip only
                {ldhAbs, 0, 0, 12},
                {jeq, 0, 1, 0x0800},
                // udp only
                {ldbAbs, 0, 0, 23}, // 14 + 9 = proto byte
                {jeq, 0, 0, 17},
                {ret, 0, 0, accept},
                {ret, 0, 0, 0},
        }
}

func attachBPF(fd int, prog []bpfIns) error {
        if len(prog) == 0 {
                return nil
        }
        kprogs := make([]unix.SockFilter, len(prog))
        for i, p := range prog {
                kprogs[i] = unix.SockFilter{Code: p.code, Jt: p.jt, Jf: p.jf, K: p.k}
        }
        fprog := &unix.SockFprog{Len: uint16(len(kprogs)), Filter: &kprogs[0]}
        return unix.SetsockoptSockFprog(fd, unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, fprog)
}
