//go:build !darwin

package alert

import "net"

func bindInterface(_ *net.Dialer, _ int) {}
