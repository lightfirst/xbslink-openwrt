package main

// UDP transport, mirroring xbs_udp_listener.cs: a single socket bound to
// 0.0.0.0:port carries all node-to-node traffic (DATA has priority).

import (
	"net"
	"sync"
	"time"
)

type outBuf struct {
	addr *net.UDPAddr
	buf  []byte
}

type Transport struct {
	conn *net.UDPConn
	app  *App

	outHP chan outBuf // DATA frames
	out   chan outBuf // control messages

	wg      sync.WaitGroup
	stopped bool
	mu      sync.Mutex
}

func NewTransport(app *App) (*Transport, error) {
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: app.cfg.Port}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}
	t := &Transport{
		conn:  conn,
		app:   app,
		outHP: make(chan outBuf, 1024),
		out:   make(chan outBuf, 512),
	}
	return t, nil
}

func (t *Transport) Port() int {
	return t.conn.LocalAddr().(*net.UDPAddr).Port
}

func (t *Transport) Start(stop <-chan struct{}) {
	t.wg.Add(2)
	go t.readLoop(stop)
	go t.writeLoop(stop)
}

func (t *Transport) Stop() {
	t.mu.Lock()
	t.stopped = true
	t.mu.Unlock()
	t.conn.Close()
	t.wg.Wait()
}

// SendTo enqueues a message for a node. highPrio is used for DATA frames.
func (t *Transport) SendTo(addr *net.UDPAddr, typ MsgType, data []byte, highPrio bool) {
	if addr == nil {
		return
	}
	buf := MarshalMessage(typ, data)
	b := outBuf{addr: addr, buf: buf}
	if highPrio {
		select {
		case t.outHP <- b:
		default:
			Log.Warnf("out queue full, dropping %v to %s", typ, addr)
		}
	} else {
		select {
		case t.out <- b:
		default:
			Log.Warnf("out queue full, dropping %v to %s", typ, addr)
		}
	}
}

func (t *Transport) readLoop(stop <-chan struct{}) {
	defer t.wg.Done()
	buf := make([]byte, 65535)
	for {
		n, from, err := t.conn.ReadFromUDP(buf)
		if err != nil {
			t.mu.Lock()
			st := t.stopped
			t.mu.Unlock()
			if st {
				return
			}
			select {
			case <-stop:
				return
			default:
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if n < 1 {
			continue
		}
		pkt := make([]byte, n)
		copy(pkt, buf[:n])
		typ, data, ok := ParseMessage(pkt)
		if !ok {
			Log.Debugf("dropping malformed packet from %s (%d bytes)", from, n)
			continue
		}
		stats.addPktIn()
		if typ == MsgData {
			// fast path: hand straight to the capture injector
			t.app.handleData(from, data)
		} else {
			t.app.enqueueControl(from, typ, data)
		}
	}
}

func (t *Transport) writeLoop(stop <-chan struct{}) {
	defer t.wg.Done()
	for {
		// DATA first
		select {
		case b := <-t.outHP:
			t.write(b)
			continue
		default:
		}
		select {
		case <-stop:
			return
		case b := <-t.outHP:
			t.write(b)
		case b := <-t.out:
			t.write(b)
		}
	}
}

func (t *Transport) write(b outBuf) {
	n, err := t.conn.WriteToUDP(b.buf, b.addr)
	if err != nil {
		Log.Debugf("send to %s failed: %v", b.addr, err)
		return
	}
	stats.addPktOut()
	_ = n
}
