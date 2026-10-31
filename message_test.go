package main

// Unit tests for the wire codec (compatibility critical).

import (
        "bytes"
        "net"
        "testing"
        "time"
)

func TestMessageRoundTrip(t *testing.T) {
        payloads := map[MsgType][]byte{
                MsgAnnounce:  []byte("CLOUD\ttestcloud"),
                MsgAddNode:   MarshalIPPort(net.IPv4(192, 168, 1, 5), 31415),
                MsgPing:      NETTicks(time.Now()),
                MsgData:      bytes.Repeat([]byte{0xAA}, 1500),
                MsgNickname:  []byte("OpenWrt"),
                MsgKnownNode: MarshalIPPort(net.IPv4(10, 0, 0, 1), 31415),
        }
        for typ, data := range payloads {
                msg := MarshalMessage(typ, data)
                if msg[0] != byte(typ) {
                        t.Fatalf("type mismatch %v != %v", msg[0], typ)
                }
                gotTyp, gotData, ok := ParseMessage(msg)
                if !ok {
                        t.Fatalf("parse failed for type %v", typ)
                }
                if gotTyp != typ {
                        t.Fatalf("type roundtrip %v != %v", gotTyp, typ)
                }
                if !bytes.Equal(gotData, data) {
                        t.Fatalf("payload roundtrip failed for type %v (%d vs %d bytes)", typ, len(gotData), len(data))
                }
        }
}

// The original C# client serializes null-payload messages as exactly ONE byte
// (xbs_node_message.getByteArray with data == null) and its udp_receiver
// accepts 1..3-byte datagrams as zero-payload messages. Interop with real
// XBSlink GUIs depends on both behaviors.
func TestShortDatagramInterop(t *testing.T) {
        // empty payload must marshal to the bare type byte
        if got := MarshalMessage(MsgAnnounce, nil); !bytes.Equal(got, []byte{0x00}) {
                t.Fatalf("1-byte ANNOUNCE expected, got %v", got)
        }
        if got := MarshalMessage(MsgGetNickname, []byte{}); !bytes.Equal(got, []byte{0x0C}) {
                t.Fatalf("1-byte GETNICKNAME expected, got %v", got)
        }
        // and the 1-byte datagrams the original GUI sends must parse
        for _, raw := range [][]byte{
                {0x00}, // ANNOUNCE (button_announce_Click, no cloud joined)
                {0x01}, // GETNODELIST
                {0x08}, // GETCLIENTVERSION
                {0x0C}, // GETNICKNAME
                {0xFF, 0x00, 0x00}, // SERVERHELLO from cloudlist/index.php
        } {
                typ, data, ok := ParseMessage(raw)
                if !ok || len(data) != 0 || int(typ) != int(raw[0]) {
                        t.Fatalf("short datagram %v not accepted (ok=%v type=%v)", raw, ok, typ)
                }
        }
        // still rejected: truly empty datagram
        if _, _, ok := ParseMessage(nil); ok {
                t.Fatal("empty datagram accepted")
        }
        // long datagrams keep the strict length check
        bad := MarshalMessage(MsgPing, make([]byte, 8))
        bad[1], bad[2] = 0xff, 0xff
        if _, _, ok := ParseMessage(bad); ok {
                t.Fatal("malformed long packet accepted")
        }
}

func TestIPPortRoundTrip(t *testing.T) {
        ip, port, ok := ParseIPPort(MarshalIPPort(net.IPv4(88, 77, 66, 55), 31415))
        if !ok || !ip.Equal(net.IPv4(88, 77, 66, 55)) || port != 31415 {
                t.Fatalf("ip:port roundtrip failed: %v %v %v", ip, port, ok)
        }
}

func TestOptionsRoundTrip(t *testing.T) {
        orig := map[string]string{OptionCloudName: "my cloud"}
        enc := EncodeOptions(orig)
        // wire format check: key TAB value (no trailing NUL for single option)
        if enc != "CLOUD\tmy cloud" {
                t.Fatalf("unexpected encoding %q", enc)
        }
        got := ParseOptions(enc)
        if got[OptionCloudName] != "my cloud" {
                t.Fatalf("decode failed: %v", got)
        }
        // two options separated by NUL
        two := EncodeOptions(map[string]string{"CLOUD": "a", "X": "b"})
        dec := ParseOptions(two)
        if dec["CLOUD"] != "a" || dec["X"] != "b" {
                t.Fatalf("two-option decode failed: %v (%q)", dec, two)
        }
}

func TestVersionRoundTrip(t *testing.T) {
        v := ParseVersion(MarshalVersion("0.9.6.0"))
        if v != "0.9.6.0" {
                t.Fatalf("version roundtrip: %q", v)
        }
}

func TestNETTicks(t *testing.T) {
        // .NET epoch offset: ticks for 1970-01-01 must be 621355968000000000
        b := NETTicks(time.Unix(0, 0).UTC())
        var v uint64
        for i := 7; i >= 0; i-- {
                v = v<<8 | uint64(b[i])
        }
        if v != 621355968000000000 {
                t.Fatalf("unexpected ticks for unix epoch: %d", v)
        }
}

func TestParseMAC(t *testing.T) {
        m, err := ParseMAC("AA:BB:CC:DD:EE:FF")
        if err != nil || m != [6]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF} {
                t.Fatalf("mac parse failed: %v %v", m, err)
        }
        if _, err := ParseMAC("xyz"); err == nil {
                t.Fatal("bad mac accepted")
        }
}
