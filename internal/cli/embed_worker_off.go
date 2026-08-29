//go:build !vector || gomobile

package cli

import "github.com/spf13/cobra"

// embedderCmds registers nothing without the vector build tag: no
// embedder is constructed in such a build, so there is no child to run
// (see the `vector && !gomobile` variant in embed_worker.go).
func embedderCmds() []*cobra.Command { return nil }
