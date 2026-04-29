package server

import (
	"errors"
	"net"
	"net/netip"
)

// ValidateLoopback rejects non-loopback bind addresses. v1 is loopback-only
// on purpose (see docs/02-server.md).
func ValidateLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ap, err := netip.ParseAddr(host)
	if err != nil {
		// Named hosts aren't accepted — resolving "localhost" correctly
		// across platforms isn't worth the ambiguity in v1.
		return errors.New("remote access is not supported in v1: use 127.0.0.1 or ::1")
	}
	if !ap.IsLoopback() {
		return errors.New("remote access is not supported in v1: bind a loopback address")
	}
	return nil
}
