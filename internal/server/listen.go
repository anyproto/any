package server

import (
	"errors"
	"net"
	"net/netip"
	"os"
	"strconv"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/config"
)

var listenLog = logger.NewNamed("listen")

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

// listen binds the HTTP listener. A non-zero port is the caller's: one
// attempt, no fallback. Port 0 is sticky: it first tries the port the
// previous port-0 start under root bound (listen.port), then an
// ephemeral one, and records the new port — so a host that always
// passes :0 keeps its origin across restarts, and a taken port never
// blocks boot.
func listen(addr, root string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	// An empty port binds an ephemeral one too.
	if p, err := strconv.Atoi(port); port != "" && (err != nil || p != 0) {
		return net.Listen("tcp", addr)
	}
	path := config.ListenPortPath(root)
	if saved := readListenPort(path); saved != 0 {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(saved)))
		if err == nil {
			return ln, nil
		}
		listenLog.Warn("previous listen port unavailable, binding an ephemeral one", zap.Int("port", saved), zap.Error(err))
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	bound := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	if err := os.WriteFile(path, []byte(bound+"\n"), 0o600); err != nil {
		listenLog.Warn("write listen port", zap.Error(err))
	}
	return ln, nil
}

// readListenPort returns the recorded port, 0 for every failure.
func readListenPort(path string) int {
	if p := readIntFile(path); p > 0 && p <= 65535 {
		return p
	}
	return 0
}
