package main

import (
        "flag"
        "fmt"
        "net"
        "strconv"
        "strings"
)

// Config mirrors both the original XBSlink console options and the
// OpenWrt UCI options (the init script maps UCI -> flags).
type Config struct {
        // capture
        Interface   string
        Port        int    // node-to-node UDP port (31415)
        Nickname    string // local nickname announced to peers
        Peers       []string
        SpecialMACs [][6]byte

        // filters
        ForwardHighPortBroadcasts bool // forward all UDP broadcasts with dst port >=1024
        AllowWellKnownPorts       bool // do NOT exclude ports <1024 (default: excluded)
        GatewayIPs                []net.IP
        DeDupeWindow              int // ms, injected-MAC loop-prevention window

        // cloud
        CloudServer   string
        CloudName     string
        CloudPassword string
        CloudMaxNodes int
        NodeIP        string // IP to register at the cloud ("" = auto detect)

        // status
        StatusPort int    // HTTP status port, 0 = off
        StatusFile string // status JSON file path, "" = off

        // misc
        ClientVersion string
        Verbose       bool
        ShowVersion   bool
}

func defaultConfig() *Config {
        return &Config{
                Port:          31415,
                Nickname:      "OpenWrt",
                CloudServer:   "http://10.165.0.105/cloudlist.php",
                CloudMaxNodes: 10,
                StatusPort:    31416,
                ClientVersion: protoVersion,
                DeDupeWindow:  2000,
        }
}

// ParseFlags parses command line options. Aliases of the original XBSlink
// console binary are supported (-i/-n/-p/-s/-c/-w/-m/-o) so that scripts
// written for the original still work.
func ParseFlags(args []string) (*Config, error) {
        cfg := defaultConfig()

        fs := flag.NewFlagSet(appName, flag.ContinueOnError)

        addStr := func(dst *string, names ...string) {
                for _, n := range names {
                        fs.StringVar(dst, n, *dst, "")
                }
        }
        addInt := func(dst *int, names ...string) {
                for _, n := range names {
                        fs.IntVar(dst, n, *dst, "")
                }
        }
        addBool := func(dst *bool, names ...string) {
                for _, n := range names {
                        fs.BoolVar(dst, n, *dst, "")
                }
        }

        var peers, specialMACs, gatewayIPs string

        addStr(&cfg.Interface, "interface", "i", "capture-device")
        addInt(&cfg.Port, "port", "p")
        addStr(&cfg.Nickname, "nickname", "n")
        fs.StringVar(&peers, "peers", "", "")
        addStr(&cfg.CloudServer, "cloudserver", "s")
        addStr(&cfg.CloudName, "cloudname", "c")
        addStr(&cfg.CloudPassword, "password", "w")
        addInt(&cfg.CloudMaxNodes, "max-nodes", "m")
        fs.StringVar(&cfg.NodeIP, "node-ip", "", "")
        fs.StringVar(&specialMACs, "special-macs", "", "")
        addBool(&cfg.ForwardHighPortBroadcasts, "forward-high-port-broadcasts", "forward-all-high-port-broadcasts")
        addBool(&cfg.AllowWellKnownPorts, "allow-wellknown-ports")
        fs.StringVar(&gatewayIPs, "gateway-ips", "", "")
        addInt(&cfg.StatusPort, "status-port")
        fs.StringVar(&cfg.StatusFile, "status-file", "", "")
        fs.StringVar(&cfg.ClientVersion, "client-version", cfg.ClientVersion, "")
        addInt(&cfg.DeDupeWindow, "dedupe-window")
        addBool(&cfg.Verbose, "verbose", "debug")
        addBool(&cfg.ShowVersion, "version", "v")

        if err := fs.Parse(args); err != nil {
                return nil, err
        }
        if cfg.ShowVersion {
                return cfg, nil
        }
        if cfg.Interface == "" {
                return nil, fmt.Errorf("capture --interface is required (e.g. br-lan)")
        }

        if peers != "" {
                for _, p := range strings.FieldsFunc(peers, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
                        p = strings.TrimSpace(p)
                        if p == "" {
                                continue
                        }
                        if !strings.Contains(p, ":") {
                                p = p + ":" + strconv.Itoa(cfg.Port)
                        }
                        if _, _, err := net.SplitHostPort(p); err != nil {
                                return nil, fmt.Errorf("bad peer %q: %v", p, err)
                        }
                        cfg.Peers = append(cfg.Peers, p)
                }
        }
        if specialMACs != "" {
                for _, m := range strings.Split(specialMACs, ",") {
                        mac, err := ParseMAC(strings.TrimSpace(m))
                        if err != nil {
                                return nil, fmt.Errorf("bad special mac %q: %v", m, err)
                        }
                        cfg.SpecialMACs = append(cfg.SpecialMACs, mac)
                }
        }
        if gatewayIPs != "" {
                for _, g := range strings.Split(gatewayIPs, ",") {
                        ip := net.ParseIP(strings.TrimSpace(g))
                        if ip == nil {
                                return nil, fmt.Errorf("bad gateway ip %q", g)
                        }
                        cfg.GatewayIPs = append(cfg.GatewayIPs, ip.To4())
                }
        }
        if cfg.CloudMaxNodes <= 0 {
                cfg.CloudMaxNodes = 10
        }
        if cfg.Port <= 0 || cfg.Port > 65535 {
                return nil, fmt.Errorf("bad --port %d", cfg.Port)
        }
        return cfg, nil
}
