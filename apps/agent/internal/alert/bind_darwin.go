//go:build darwin

package alert

import (
	"golang.org/x/sys/unix"
	"net"
	"syscall"
)

// Source binding alone does not override TUN routes. Darwin's IP_BOUND_IF
// constrains the socket to the selected physical interface as well.
func bindInterface(d *net.Dialer, index int) {
	d.Control = func(_, _ string, raw syscall.RawConn) error {
		var err error
		if controlErr := raw.Control(func(fd uintptr) { err = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_BOUND_IF, index) }); controlErr != nil {
			return controlErr
		}
		return err
	}
}
