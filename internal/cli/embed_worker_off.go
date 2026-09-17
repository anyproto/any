//go:build !llamacpp || android || ios

package cli

import "github.com/spf13/cobra"

// embedderCmds registers nothing in a build without the local embedder
// (no `llamacpp` tag, or a mobile target): there is no child to run
// (see embed_worker.go).
func embedderCmds() []*cobra.Command { return nil }
