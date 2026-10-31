package main

// Status reporting: HTTP JSON endpoint + status file for LuCI.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Status struct {
	app *App

	mu        sync.Mutex
	dirty     bool
	writePath string
}

type statusNode struct {
	IP        string   `json:"ip"`
	Port      int      `json:"port"`
	Nickname  string   `json:"nickname"`
	Version   string   `json:"version"`
	DelayMS   int32    `json:"ping_ms"`
	Xboxes    []string `json:"devices"`
	RxPkts    uint64   `json:"rx_packets"`
	TxPkts    uint64   `json:"tx_packets"`
	RxBytes   uint64   `json:"rx_bytes"`
	TxBytes   uint64   `json:"tx_bytes"`
	Connected bool     `json:"connected"`
}

type statusJSON struct {
	App       string       `json:"app"`
	Version   string       `json:"version"`
	Proto     string       `json:"protocol_version"`
	UptimeSec int64        `json:"uptime_sec"`
	Nickname  string       `json:"nickname"`
	LocalIP   string       `json:"local_ip"`
	LocalPort int          `json:"local_port"`
	Interface string       `json:"interface"`
	PortOpen  bool         `json:"port_reachable"`
	Nodes     []statusNode `json:"nodes"`
	LocalMACs []string     `json:"local_devices"`
	Cloud     struct {
		Enabled   bool   `json:"enabled"`
		Joined    bool   `json:"joined"`
		Name      string `json:"name"`
		Reachable bool   `json:"reachable"`
	} `json:"cloud"`
	Stats struct {
		Captured uint64 `json:"frames_captured"`
		Injected uint64 `json:"frames_injected"`
		PktIn    uint64 `json:"udp_in"`
		PktOut   uint64 `json:"udp_out"`
		NoRoute  uint64 `json:"frames_no_route"`
	} `json:"stats"`
}

func NewStatus(app *App) *Status {
	return &Status{app: app, writePath: app.cfg.StatusFile}
}

func (s *Status) MarkDirty() {
	s.mu.Lock()
	s.dirty = true
	s.mu.Unlock()
}

func (s *Status) Start(stop <-chan struct{}, wg *sync.WaitGroup) {
	if s.writePath != "" {
		os.MkdirAll(filepath.Dir(s.writePath), 0755)
	}
	if s.app.cfg.StatusPort > 0 {
		mux := http.NewServeMux()
		mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			out, _ := s.app.collectStatus().toJSON()
			w.Write(out)
		})
		srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", s.app.cfg.StatusPort), Handler: mux}
		go func() {
			<-stop
			srv.Close()
		}()
		go func() {
			Log.Infof(" * status endpoint: http://127.0.0.1:%d/status", s.app.cfg.StatusPort)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				Log.Errorf("status http server: %v", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.fileLoop(stop)
	}()
}

func (s *Status) fileLoop(stop <-chan struct{}) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	s.write()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			s.mu.Lock()
			d := s.dirty
			s.dirty = false
			s.mu.Unlock()
			if d {
				s.write()
			}
		}
	}
}

func (s *Status) write() {
	if s.writePath == "" {
		return
	}
	out, err := s.app.collectStatus().toJSON()
	if err != nil {
		return
	}
	tmp := s.writePath + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return
	}
	os.Rename(tmp, s.writePath)
}

func (st *statusJSON) toJSON() ([]byte, error) {
	return json.MarshalIndent(st, "", "  ")
}

func (a *App) collectStatus() *statusJSON {
	out := &statusJSON{
		App:       appName,
		Version:   appVersion,
		Proto:     protoVersion,
		Nickname:  a.cfg.Nickname,
		LocalIP:   a.localIP().String(),
		LocalPort: int(a.localPort()),
		Interface: a.cfg.Interface,
	}
	out.UptimeSec = stats.startedAt.Load()
	if out.UptimeSec > 0 {
		out.UptimeSec = nowUnix() - out.UptimeSec
	}
	out.PortOpen = a.portReachable.Load()

	out.Stats.Captured = stats.captured.Load()
	out.Stats.Injected = stats.injected.Load()
	out.Stats.PktIn = stats.pktIn.Load()
	out.Stats.PktOut = stats.pktOut.Load()
	out.Stats.NoRoute = stats.noRoute.Load()

	for mac, ip := range a.capture.SniffedMACsMap() {
		s := macString(mac)
		if ip != nil {
			s += " (" + ip.String() + ")"
		}
		out.LocalMACs = append(out.LocalMACs, s)
	}
	if out.LocalMACs == nil {
		out.LocalMACs = []string{}
	}

	for _, n := range a.nodes.All() {
		sn := statusNode{
			IP:        n.IPPublic().String(),
			Port:      int(n.PortPublic()),
			Nickname:  n.Nickname(),
			Version:   n.Version(),
			DelayMS:   n.DelayMS(),
			RxPkts:    n.RxPackets(),
			TxPkts:    n.TxPackets(),
			RxBytes:   n.RxBytes(),
			TxBytes:   n.TxBytes(),
			Connected: n.sincePong() < maxPingNoResponse,
		}
		for mac, ip := range n.XboxList() {
			s := macString(mac)
			if ip != nil {
				s += " (" + ip.String() + ")"
			}
			sn.Xboxes = append(sn.Xboxes, s)
		}
		if sn.Xboxes == nil {
			sn.Xboxes = []string{}
		}
		out.Nodes = append(out.Nodes, sn)
	}
	if out.Nodes == nil {
		out.Nodes = []statusNode{}
	}

	if a.cloud != nil {
		joined, name, _, reachable := a.cloud.Info()
		out.Cloud.Enabled = true
		out.Cloud.Joined = joined
		out.Cloud.Name = name
		out.Cloud.Reachable = reachable
	}
	return out
}
