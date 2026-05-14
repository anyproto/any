//go:build android || gomobile

// Package mobile is the gomobile bind surface for embedding the any HTTP
// server inside an Android (or, in principle, iOS) process. Only basic
// types cross the bridge — keep the surface flat.
package mobile

import (
	"context"
	"errors"
	"sync"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/server"
	"github.com/anyproto/any/internal/version"
)

var (
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan error
	addr   string
)

// Start boots the any server. dataDir maps to Context.getFilesDir() on Android.
// listenAddr is typically "127.0.0.1:7001"; pass "127.0.0.1:0" to let the OS
// pick a free port (read it back with Address()).
//
// Returns once the server has started accepting connections (or failed to).
// Subsequent calls while a server is running return an error.
func Start(dataDir, listenAddr string) error {
	mu.Lock()
	defer mu.Unlock()
	if cancel != nil {
		return errors.New("any: server already running")
	}

	cfg := config.Defaults()
	cfg.DataDir = dataDir
	cfg.Listen.Addr = listenAddr

	ctx, c := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() {
		ch <- server.Run(ctx, cfg)
	}()

	cancel = c
	done = ch
	addr = listenAddr
	return nil
}

// Stop signals graceful shutdown and waits for the server goroutine to exit.
// Safe to call when the server is not running.
func Stop() error {
	mu.Lock()
	c, ch := cancel, done
	cancel, done = nil, nil
	addr = ""
	mu.Unlock()

	if c == nil {
		return nil
	}
	c()
	return <-ch
}

// Address returns the listen address Start was called with. Empty when the
// server is not running.
func Address() string {
	mu.Lock()
	defer mu.Unlock()
	return addr
}

// Version returns the build-stamped version string ("any <version> (commit
// <sha>, built <ts>)").
func Version() string {
	return version.String()
}
