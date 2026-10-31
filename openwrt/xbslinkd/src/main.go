// Package main: xbslinkd - a Go rewrite of XBSlink (seuffert/XBSlink) core,
// targeted at OpenWrt and other embedded Linux systems.
//
// Wire-protocol compatible with XBSlink v0.9.6.0 (see docs/PROTOCOL.md).
// SPDX-License-Identifier: GPL-2.0-or-later
package main

import (
        "fmt"
        "os"
        "os/signal"
        "syscall"
        "time"
)

const (
        protoVersion = "0.9.6.0" // reported to peers/cloud for max compatibility
        appName      = "xbslinkd"
)

// appVersion can be overridden at link time:
// -ldflags "-X github.com/xbslink/xbslinkd.appVersion=1.2.3"
var appVersion = "1.0.0"

func main() {
        cfg, err := ParseFlags(os.Args[1:])
        if err != nil {
                fmt.Fprintf(os.Stderr, "%s: %v\n", appName, err)
                os.Exit(2)
        }
        if cfg.ShowVersion {
                fmt.Printf("%s %s (XBSlink protocol %s)\n", appName, appVersion, protoVersion)
                return
        }
        NewLogger(cfg.Verbose)

        app, err := NewApp(cfg)
        if err != nil {
                Log.Fatalf("startup failed: %v", err)
        }

        sigCh := make(chan os.Signal, 4)
        signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

        Log.Infof("%s %s starting (XBSlink protocol %s)", appName, appVersion, protoVersion)
        if err := app.Start(); err != nil {
                Log.Fatalf("startup failed: %v", err)
        }

        <-sigCh
        Log.Infof("shutdown signal received, saying goodbye to peers...")
        go func() {
                time.Sleep(3 * time.Second)
                Log.Fatalf("shutdown timed out")
        }()
        app.Stop()
        Log.Infof("bye")
}
