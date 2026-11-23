package cli

import (
	"fmt"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
)

// neuralEngineResult is `any debug neural-engine`'s stdout.
type neuralEngineResult struct {
	*indexer.NeuralEngineReport
	// EmbedderPids are the embed children running on this machine during
	// the measurement, which Note explains.
	EmbedderPids []int  `json:"embedderPids,omitempty"`
	Note         string `json:"note,omitempty"`
}

// `any debug neural-engine` measures the local embedder's Core ML
// package on this machine: where Core ML plans each function and how
// fast it loads and predicts. No HTTP and no server: it runs Core ML in
// this process, on a copy it compiles into a temporary directory.
func newDebugNeuralEngineCmd() *cobra.Command {
	var functions []string
	c := &cobra.Command{
		Use:   "neural-engine",
		Short: "measure the local embedder's Core ML functions on this Mac: compute plan, load and predict times (no server needed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadClient(configFlags())
			if err != nil {
				return err
			}
			// An interrupt ends the run with its temporary directory removed.
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			progress := func(format string, args ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...)
			}
			out := neuralEngineResult{EmbedderPids: embedderChildren()}
			if len(out.EmbedderPids) > 0 {
				out.Note = "an embedder child was running: it shares the Neural Engine, so the times here can be higher"
				progress("note: %s", out.Note)
			}
			models := config.ModelsDir(config.ExpandTilde(cfg.DataDir))
			out.NeuralEngineReport, err = indexer.DebugNeuralEngine(ctx, models, functions, progress)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addDataDirFlags(c)
	c.Flags().StringSliceVar(&functions, "function", nil, "measure only this function (s128, s256, …); repeatable")
	return c
}

// embedderChildren lists the `any run embedder` processes on this
// machine; none where they cannot be listed.
func embedderChildren() []int {
	raw, err := exec.Command("pgrep", "-f", "run embedder --model").Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		if pid, err := strconv.Atoi(field); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}
