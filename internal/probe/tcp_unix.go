//go:build !windows

package probe

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// setTCPTTL returns a Control function that sets IP_TTL on the TCP socket.
func setTCPTTL(ttl int) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var setsockoptErr error
		err := c.Control(func(fd uintptr) {
			setsockoptErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_TTL, ttl)
		})
		if err != nil {
			return err
		}
		return setsockoptErr
	}
}
