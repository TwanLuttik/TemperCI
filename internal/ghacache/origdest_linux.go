//go:build linux

package ghacache

import (
	"encoding/binary"
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func originalDest(c net.Conn) (string, error) {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return "", fmt.Errorf("not tcp")
	}
	rc, err := tc.SyscallConn()
	if err != nil {
		return "", err
	}
	var dest string
	var sysErr error
	err = rc.Control(func(fd uintptr) {
		addr, err := unix.GetsockoptIPv6Mreq(int(fd), syscall.IPPROTO_IP, unix.SO_ORIGINAL_DST)
		if err != nil {
			sysErr = err
			return
		}
		ip := net.IP(addr.Multiaddr[4:8])
		port := binary.BigEndian.Uint16(addr.Multiaddr[2:4])
		dest = net.JoinHostPort(ip.String(), fmt.Sprintf("%d", port))
	})
	if err != nil {
		return "", err
	}
	return dest, sysErr
}
