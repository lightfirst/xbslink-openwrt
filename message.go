package main

// Wire protocol codec, byte-for-byte compatible with the original C#
// implementation (xbs_node_message.cs). All multi byte integers are
// LITTLE-ENDIAN (C# BitConverter on x86).

import (
        "encoding/binary"
        "fmt"
        "net"
        "strings"
        "time"
)

// MsgType enumerates the xbs_node_message_type values.
type MsgType byte

const (
        MsgAnnounce         MsgType = 0x00
        MsgGetNodeList      MsgType = 0x01
        MsgAddNode          MsgType = 0x02
        MsgDelNode          MsgType = 0x03
        MsgPing             MsgType = 0x04
        MsgPong             MsgType = 0x05
        MsgData             MsgType = 0x06
        MsgAddMasterBrowser MsgType = 0x07 // obsolete
        MsgGetClientVersion MsgType = 0x08
        MsgClientVersion    MsgType = 0x09
        MsgChatMsg          MsgType = 0x0A
        MsgNickname         MsgType = 0x0B
        MsgGetNickname      MsgType = 0x0C
        MsgKnownNode        MsgType = 0x0D
        MsgToCloudHelper    MsgType = 0x0E
        MsgFromCloudHelper  MsgType = 0x0F
        MsgServerHello      MsgType = 0xFF
)

const msgHeaderLen = 3 // type(1) + len(2)

// Option keys inside ANNOUNCE payloads.
const (
        OptionCloudName = "CLOUD"
        optKeySep       = '\t'   // key=value separator (C# OPTIONS_EQUALS_CHAR)
        optPairSep      = '\x00' // pair separator       (C# OPTIONS_DELIMITER_CHAR)
)

// MarshalMessage encodes a message. A nil/empty payload is serialized as
// just the single type byte, byte-for-byte like the original C#
// getByteArray() (getMessageByteArraySize() returns 1 when data == null) —
// the original sends e.g. ANNOUNCE/GETNODELIST/GETNICKNAME as 1-byte
// datagrams when no cloud is joined.
func MarshalMessage(t MsgType, data []byte) []byte {
        if len(data) == 0 {
                return []byte{byte(t)}
        }
        buf := make([]byte, msgHeaderLen+len(data))
        buf[0] = byte(t)
        binary.LittleEndian.PutUint16(buf[1:3], uint16(len(data)))
        copy(buf[3:], data)
        return buf
}

// ParseMessage decodes a datagram, mirroring the original udp_receiver
// (xbs_udp_listener.cs): only datagrams LONGER than 3 bytes are length
// validated (data_len + 3 must equal the datagram size); datagrams of 1..3
// bytes are accepted as zero-payload messages. The original GUI sends 1-byte
// ANNOUNCE/GETCLIENTVERSION/GETNICKNAME datagrams when not in a cloud, so
// rejecting short datagrams would break interop with real XBSlink clients.
func ParseMessage(b []byte) (MsgType, []byte, bool) {
        if len(b) < 1 {
                return 0, nil, false
        }
        t := MsgType(b[0])
        if len(b) <= msgHeaderLen {
                return t, nil, true
        }
        dl := int(binary.LittleEndian.Uint16(b[1:3]))
        if dl+msgHeaderLen != len(b) {
                return 0, nil, false
        }
        return t, b[msgHeaderLen:], true
}

// MarshalIPPort encodes "ip:port" payload used by ADDNODE/DELNODE/KNOWNNODE/
// cloudhelper messages: 4 bytes IPv4 + 2 bytes port LE.
func MarshalIPPort(ip net.IP, port uint16) []byte {
        b := make([]byte, 6)
        copy(b[0:4], ip.To4())
        binary.LittleEndian.PutUint16(b[4:6], port)
        return b
}

func ParseIPPort(b []byte) (net.IP, uint16, bool) {
        if len(b) < 6 {
                return nil, 0, false
        }
        ip := make(net.IP, 4)
        copy(ip, b[0:4])
        return ip, binary.LittleEndian.Uint16(b[4:6]), true
}

// EncodeOptions builds the ANNOUNCE options string:
// "key\tvalue" pairs separated by NUL bytes.
func EncodeOptions(opts map[string]string) string {
        var sb strings.Builder
        first := true
        for k, v := range opts {
                if !first {
                        sb.WriteByte(optPairSep)
                }
                sb.WriteString(k)
                sb.WriteByte(optKeySep)
                sb.WriteString(v)
                first = false
        }
        return sb.String()
}

// ParseOptions decodes an ANNOUNCE options string.
func ParseOptions(s string) map[string]string {
        opts := make(map[string]string)
        if s == "" {
                return opts
        }
        for _, pair := range strings.Split(s, string(optPairSep)) {
                kv := strings.SplitN(pair, string(optKeySep), 2)
                if len(kv) != 2 {
                        continue
                }
                opts[kv[0]] = kv[1]
        }
        return opts
}

// MarshalVersion encodes "0.9.6.0" into the 4 byte CLIENTVERSION payload.
func MarshalVersion(v string) []byte {
        b := []byte{0, 0, 0, 0}
        var a, b2, c, d int
        if _, err := fmt.Sscanf(v, "%d.%d.%d.%d", &a, &b2, &c, &d); err == nil {
                b[0], b[1], b[2], b[3] = byte(a), byte(b2), byte(c), byte(d)
        }
        return b
}

func ParseVersion(b []byte) string {
        if len(b) < 4 {
                return "(unknown)"
        }
        return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
}

// NETTicks encodes a Go time into the 8 byte .NET DateTime.Ticks value
// (100ns intervals since 0001-01-01), little endian. Content is opaque on
// the wire (the peer just echoes it) but we stay byte-compatible anyway.
func NETTicks(t time.Time) []byte {
        const epochDiff100ns = 621355968000000000 // 0001-01-01 .. 1970-01-01
        nanos := t.UnixNano()
        ticks := nanos/100 + epochDiff100ns
        b := make([]byte, 8)
        binary.LittleEndian.PutUint64(b, uint64(ticks))
        return b
}

// ParseMAC parses aa:bb:cc:dd:ee:ff / aa-bb-cc-dd-ee-ff / aabbccddeeff.
func ParseMAC(s string) ([6]byte, error) {
        var m [6]byte
        s = strings.NewReplacer(":", "", "-", "", ".", "").Replace(s)
        if len(s) != 12 {
                return m, fmt.Errorf("bad length")
        }
        for i := 0; i < 6; i++ {
                _, err := fmt.Sscanf(s[i*2:i*2+2], "%02x", &m[i])
                if err != nil {
                        return m, err
                }
        }
        return m, nil
}
