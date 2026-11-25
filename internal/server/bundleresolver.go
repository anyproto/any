package server

import (
	"context"

	"github.com/anyproto/any/internal/bundles"
)

// backgroundCtx is the process-lifetime context for work that must
// outlive the request that started it — bounded by shutdown, never by
// a client disconnect.
func (d *deps) backgroundCtx() context.Context {
	if d.shutdownCtx != nil {
		return d.shutdownCtx
	}
	return context.Background()
}

// bundleResolver returns the process's bundles engine, building it on
// first use.
func (d *deps) bundleResolver() *bundles.Resolver {
	d.installsOnce.Do(func() {
		d.installs = bundles.NewResolver(bundles.DefaultGrace)
	})
	return d.installs
}
