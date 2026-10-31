package main

// Cloud (cloudlist) HTTP client, mirroring xbs_cloudlist.cs. The server side
// reference implementation is included in cloudlist/index.php.

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const cloudUpdateInterval = 29 * time.Second

type Cloud struct {
	app *App

	mu        sync.Mutex
	joined    bool
	name      string
	uuid      string
	reachable bool
	lastResp  string

	client *http.Client
}

func NewCloud(app *App) *Cloud {
	return &Cloud{
		app: app,
		client: &http.Client{
			Timeout: 8 * time.Second,
		},
	}
}

func (c *Cloud) JoinedName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.joined {
		return c.name
	}
	return ""
}

func (c *Cloud) IsReachable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reachable
}

func (c *Cloud) Info() (joined bool, name, uuid string, reachable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.joined, c.name, c.uuid, c.reachable
}

func (c *Cloud) Start(stop <-chan struct{}, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.run(stop)
	}()
}

func (c *Cloud) run(stop <-chan struct{}) {
	// 1) probe port reachability via SENDHELLO (arm the UDP listener FIRST,
	// then issue the HTTP request - the SERVERHELLO packet can arrive first)
	Log.Infof(" * probing port reachability via SENDHELLO...")
	c.app.armHelloProbe()
	_, err := c.request(url.Values{
		"cmd":       {"SENDHELLO"},
		"node_ip":   {c.app.localIP().String()},
		"node_port": {fmt.Sprint(c.app.localPort())},
	})
	if err != nil {
		Log.Warnf("SENDHELLO request failed: %v", err)
	} else if c.app.waitHelloSignal(5 * time.Second) {
		c.mu.Lock()
		c.reachable = true
		c.mu.Unlock()
		Log.Infof(" * node port %d/udp is REACHABLE from the internet", c.app.localPort())
	} else {
		Log.Warnf(" * node port %d/udp NOT reachable (open it in the firewall for best results)", c.app.localPort())
	}
	c.app.portReachable.Store(c.IsReachable())

	// 2) join
	if err := c.Join(); err != nil {
		Log.Errorf("cloud join failed: %v (will retry every 60s)", err)
	}

	// 3) update loop
	tick := time.NewTicker(cloudUpdateInterval)
	retry := time.NewTicker(60 * time.Second)
	defer tick.Stop()
	defer retry.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			if err := c.Update(); err != nil {
				Log.Errorf("cloud update failed: %v", err)
			}
		case <-retry.C:
			if !c.JoinedNameBool() {
				if err := c.Join(); err != nil {
					Log.Errorf("cloud re-join failed: %v", err)
				}
			}
		}
	}
}

func (c *Cloud) JoinedNameBool() bool { return c.JoinedName() != "" }

// Join performs cmd=JOIN (creates the cloud when it does not exist yet).
func (c *Cloud) Join() error {
	cfg := c.app.cfg
	password := ""
	if cfg.CloudPassword != "" {
		sum := md5.Sum([]byte(cfg.CloudPassword))
		password = strings.ToUpper(hex.EncodeToString(sum[:]))
	}
	q := url.Values{
		"cmd":           {"JOIN"},
		"cloudname":     {cfg.CloudName},
		"maxnodes":      {fmt.Sprint(cfg.CloudMaxNodes)},
		"password":      {password},
		"node_ip":       {c.app.localIP().String()},
		"node_port":     {fmt.Sprint(c.app.localPort())},
		"nick":          {cfg.Nickname},
		"reachable":     {"0"},
		"getallnodes":   {"1"},
		"clientversion": {cfg.ClientVersion},
	}
	if c.IsReachable() {
		q.Set("reachable", "1")
	}
	result, err := c.request(q)
	if err != nil {
		return err
	}
	c.lastResp = result
	rows := splitLines(result)
	if len(rows) < 1 || !strings.HasPrefix(rows[0], "OK:") {
		return fmt.Errorf("server said: %s", firstLine(result))
	}
	uuid := strings.TrimPrefix(rows[0], "OK:")
	if uuid == "" {
		return fmt.Errorf("server returned empty uuid")
	}

	c.mu.Lock()
	c.joined = true
	c.name = cfg.CloudName
	c.uuid = uuid
	c.mu.Unlock()
	Log.Infof(" x joined cloud %q (uuid %s)", cfg.CloudName, uuid)

	nodes := 0
	for _, row := range rows[1:] {
		if ip, port, ok := parseNodeRow(row); ok {
			nodes++
			c.app.nodes.TryAddingNode(ip, port)
		}
	}
	if nodes > 0 {
		Log.Infof(" x cloud returned %d node(s), announcing...", nodes)
	}
	c.app.statusDirty()
	return nil
}

// Update performs cmd=UPDATE and adds newly discovered nodes.
func (c *Cloud) Update() error {
	if c.JoinedName() == "" {
		return nil
	}
	c.mu.Lock()
	name, uuid := c.name, c.uuid
	c.mu.Unlock()
	result, err := c.request(url.Values{
		"cmd":       {"UPDATE"},
		"cloudname": {name},
		"uuid":      {uuid},
	})
	if err != nil {
		return err
	}
	c.lastResp = result
	rows := splitLines(result)
	if len(rows) < 1 || !strings.HasPrefix(rows[0], "OK:") {
		if len(rows) > 0 {
			return fmt.Errorf("server said: %s", rows[0])
		}
		return fmt.Errorf("empty update response")
	}
	for _, row := range rows[1:] {
		if ip, port, ok := parseNodeRow(row); ok {
			if c.app.nodes.findNode(ip, port) == nil {
				Log.Debugf("new cloud node %s:%d", ip, port)
				c.app.nodes.TryAddingNode(ip, port)
			}
		}
	}
	return nil
}

// Leave performs cmd=LEAVE (best effort, called on shutdown).
func (c *Cloud) Leave() {
	c.mu.Lock()
	joined := c.joined
	name, uuid := c.name, c.uuid
	c.joined = false
	c.mu.Unlock()
	if !joined {
		return
	}
	_, err := c.request(url.Values{
		"cmd":       {"LEAVE"},
		"cloudname": {name},
		"uuid":      {uuid},
	})
	if err != nil {
		Log.Warnf("cloud leave failed: %v", err)
	} else {
		Log.Infof(" x left cloud %q", name)
	}
}

func (c *Cloud) request(q url.Values) (string, error) {
	u := c.app.cfg.CloudServer + "?" + q.Encode()
	resp, err := c.client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func parseNodeRow(row string) (ip net.IP, port uint16, ok bool) {
	// node_ip=1.2.3.4&node_port=31415
	var ipStr, portStr string
	for _, kv := range strings.Split(row, "&") {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "node_ip":
			ipStr = parts[1]
		case "node_port":
			portStr = parts[1]
		}
	}
	if ipStr == "" || portStr == "" {
		return nil, 0, false
	}
	ip = net.ParseIP(unescapeQuery(ipStr)).To4()
	if ip == nil {
		return nil, 0, false
	}
	p := 0
	for _, ch := range portStr {
		if ch < '0' || ch > '9' {
			return nil, 0, false
		}
		p = p*10 + int(ch-'0')
	}
	if p <= 0 || p > 65535 {
		return nil, 0, false
	}
	return ip, uint16(p), true
}

func unescapeQuery(s string) string {
	if u, err := url.QueryUnescape(s); err == nil {
		return u
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func splitLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}
