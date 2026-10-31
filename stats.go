package main

import (
	"sync/atomic"
	"time"
)

// Stats holds global counters for the status page.
type Stats struct {
	pktIn     atomic.Uint64
	pktOut    atomic.Uint64
	captured  atomic.Uint64
	injected  atomic.Uint64
	forwarded atomic.Uint64
	noRoute   atomic.Uint64
	startedAt atomic.Int64 // unix
}

var stats = &Stats{}

func (s *Stats) Init()        { s.startedAt.Store(nowUnix()) }
func (s *Stats) addPktIn()    { s.pktIn.Add(1) }
func (s *Stats) addPktOut()   { s.pktOut.Add(1) }
func (s *Stats) addCaptured() { s.captured.Add(1) }
func (s *Stats) addInjected() { s.injected.Add(1) }

func (s *Stats) droppedNoRoute() { s.noRoute.Add(1) }
func (s *Stats) addNoRoute()     { s.noRoute.Add(1) }

func nowUnix() int64 { return time.Now().Unix() }
