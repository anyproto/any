package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
)

// neuralEngineResult is `any debug neural-engine`'s stdout.
type neuralEngineResult struct {
	*indexer.NeuralEngineReport
	// EmbedderPids are the embedder children running Core ML when the
	// command started, which Note explains.
	EmbedderPids []int  `json:"embedderPids,omitempty"`
	Note         string `json:"note,omitempty"`
}

// `any debug neural-engine` measures the local embedder's Core ML package
// in this process, on a copy compiled into a temporary directory: no
// server, no HTTP.
func newDebugNeuralEngineCmd() *cobra.Command {
	var functions []string
	c := &cobra.Command{
		Use:   "neural-engine",
		Short: "measure the local embedder's Core ML functions on this Mac: compute plan, load and predict times (no server needed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Until the process exits, a reader that went away (`| head`)
			// fails a write instead of killing it: the directory's removal
			// and the error line, a config's included, still come.
			signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE)
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			cleanedUp := exitOnSecondInterrupt(cancel)
			cfg, err := config.LoadClient(configFlags())
			if err != nil {
				cleanedUp() // nothing to remove yet
				return err
			}
			progress := func(format string, args ...any) {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...) // a closed stderr does not end the run
			}
			out := neuralEngineResult{EmbedderPids: coreMLEmbedderChildren()}
			if len(out.EmbedderPids) > 0 {
				out.Note = "an embedder child running Core ML was present when the command started: it may contend for the Neural Engine, so the times here can be higher"
				progress("note: %s", out.Note)
			}
			models := config.ModelsDir(config.ExpandTilde(cfg.DataDir))
			out.NeuralEngineReport, err = indexer.DebugNeuralEngine(ctx, models, functions, progress)
			// Its temporary directory is gone: an interrupt while the result
			// or the error is written, which a full pipe can block, ends it.
			cleanedUp()
			if err == nil {
				err = ctx.Err() // an interrupt as the run ended
			}
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

// exitOnSecondInterrupt has the first SIGINT or SIGTERM cancel the run and
// the second end the process (130, 143), also when the shell ignored them;
// once cleanedUp is called, nothing is left to remove and the first ends it.
func exitOnSecondInterrupt(cancel context.CancelFunc) (cleanedUp func()) {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	var clean atomic.Bool
	go func() {
		sig := <-sigs
		// Cancelled before clean is read: a run that is marked clean after
		// that read still sees the interrupt in its context.
		cancel()
		if !clean.Load() {
			sig = <-sigs
		}
		code := 1
		if s, ok := sig.(syscall.Signal); ok {
			code = 128 + int(s)
		}
		os.Exit(code)
	}()
	return func() { clean.Store(true) }
}

// coreMLEmbedderChildren lists the `any run embedder` processes on this
// Mac started with a Core ML package (`--coreml`); none elsewhere, or
// where they cannot be listed.
func coreMLEmbedderChildren() []int {
	if runtime.GOOS != "darwin" {
		return nil
	}
	raw, err := exec.Command("pgrep", "-f", "run embedder --model .* --coreml ").Output()
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
