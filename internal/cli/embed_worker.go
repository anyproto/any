//go:build vector && !gomobile

package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/indexer"
)

// embedderCmds registers `any run embedder` — the embedding child the
// server starts for itself (docs/13-index.md § GPU offload). It is not
// client surface: the server re-execs this binary so a llama.cpp abort
// kills the child instead of the server, and speaks a framed protocol
// over stdin/stdout. Hidden, and absent from builds without the vector
// leg (embed_worker_off.go).
func embedderCmds() []*cobra.Command {
	var cfg indexer.EmbedWorkerConfig
	c := &cobra.Command{
		Use:    "embedder",
		Short:  "run the embedding worker process (internal)",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return indexer.RunEmbedWorker(cmd.Context(), cfg, os.Stdin, os.Stdout)
		},
	}
	f := c.Flags()
	f.StringVar(&cfg.ModelPath, "model", "", "GGUF model file (required)")
	f.StringVar(&cfg.LibDir, "lib-dir", "", "directory holding the llama.cpp shared libraries")
	f.StringVar(&cfg.QueryPrefix, "query-prefix", "", "instruction prepended to query texts")
	f.IntVar(&cfg.ContextSize, "ctx", 0, "context size in tokens (0 = default)")
	f.IntVar(&cfg.Threads, "threads", 0, "compute threads (0 = NumCPU-1)")
	f.IntVar(&cfg.Dim, "dim", 0, "truncate output vectors to this dimension (0 = model dim)")
	f.IntVar(&cfg.BatchDocs, "batch-docs", 0, "documents packed into one decode (0 = default)")
	f.IntVar(&cfg.GpuLayers, "gpu-layers", -1, "n_gpu_layers override (-1 = llama.cpp default, 0 = CPU only)")
	f.IntVar(&cfg.Niceness, "niceness", 0, "lower this process's scheduling priority (0 = leave it)")
	return []*cobra.Command{c}
}
