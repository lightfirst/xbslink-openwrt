package main

// Bridge port enumeration via rtnetlink (no sysfs dependency: works in
// containers, on OpenWrt, everywhere).

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// bridgePorts returns the interface names enslaved to the bridge with the
// given ifindex (empty list when `ifindex` is not a bridge or has no ports).
func bridgePorts(ifindex int) []string {
	rib, err := syscall.NetlinkRIB(unix.RTM_GETLINK, unix.AF_UNSPEC)
	if err != nil {
		Log.Debugf("netlink RTM_GETLINK failed: %v", err)
		return nil
	}
	msgs, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range msgs {
		if m.Header.Type != syscall.RTM_NEWLINK {
			continue
		}
		attrs, err := syscall.ParseNetlinkRouteAttr(&m)
		if err != nil {
			continue
		}
		name := ""
		master := 0
		for _, a := range attrs {
			switch a.Attr.Type {
			case unix.IFLA_IFNAME:
				name = string(a.Value[:len(a.Value)-1]) // strip NUL
			case unix.IFLA_MASTER:
				master = int(nativeUint32(a.Value))
			}
		}
		if name != "" && master == ifindex {
			out = append(out, name)
		}
	}
	return out
}

func nativeUint32(b []byte) uint32 {
	_ = b[3]
	if isLittleEndian() {
		return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	}
	return uint32(b[3]) | uint32(b[2])<<8 | uint32(b[1])<<16 | uint32(b[0])<<24
}

func isLittleEndian() bool {
	var x uint16 = 1
	return *(*byte)(unsafe.Pointer(&x)) == 1
}
