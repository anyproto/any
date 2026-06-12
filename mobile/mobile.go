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
// pick a free port (read it back with Address()). nodeconfYAML is the
// staging.yml contents — required, because the AAR has no filesystem
// fallback for any-sync's nodeconf.
//
// Returns once the server has bound the listener (success) or failed during
// wallet / SDK / listener setup (error). The error string is suitable for
// surfacing to the host app — it carries the underlying Go error message.
// Subsequent calls while a server is running return an error.
func Start(dataDir, listenAddr, nodeconfYAML string) error {
	mu.Lock()
	defer mu.Unlock()
	if cancel != nil {
		return errors.New("any: server already running")
	}
	if nodeconfYAML == "" {
		return errors.New("any: nodeconfYAML is required")
	}

	cfg := config.Defaults()
	cfg.DataDir = dataDir
	cfg.Listen.Addr = listenAddr
	cfg.Network.Nodeconf = nodeconfYAML
	// No indexer on mobile (yet): Defaults() enables it with the local
	// llama.cpp embedder, which would kick off a ~600MB model download
	// on device. Pre-auth-merge mobile builds never wired the indexer;
	// keep that behavior until a deliberate mobile indexing decision.
	cfg.Index.Enabled = false

	ctx, c := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	ready := make(chan string, 1)
	go func() {
		ch <- server.RunWith(ctx, cfg, server.RunOptions{
			Ready: func(boundAddr string) { ready <- boundAddr },
		})
	}()

	select {
	case boundAddr := <-ready:
		cancel = c
		done = ch
		addr = boundAddr
		return nil
	case err := <-ch:
		c()
		if err == nil {
			err = errors.New("any: server exited without binding")
		}
		return err
	}
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

// Address returns the actually-bound listen address (host:port). Differs
// from the listenAddr passed to Start when ":0" was used and the OS picked
// a port. Empty when the server is not running.
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
