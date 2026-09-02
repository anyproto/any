package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/server"
)

// stopWait bounds how long `any stop` waits for the signalled server
// to release its instance lock: the server's own graceful path is two
// 10s-bounded phases.
const stopWait = 25 * time.Second

// stopResult is `any stop`'s stdout.
type stopResult struct {
	Stopped bool   `json:"stopped"`
	Account string `json:"account,omitempty"`
	PID     int    `json:"pid"`
}

// `any stop` — stop the standalone server serving the data dir's
// account. No HTTP: the server is found by its held instance lock
// (proof of life), signalled SIGTERM — the same graceful path Ctrl-C
// takes — and waited for. Works against a wedged server and one on an
// ephemeral port; a managed server is stopped by its host instead.
func newStopCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "stop",
		Short: "stop the running server for this data dir (signal, no HTTP)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configFlags())
			if err != nil {
				return err
			}
			root := config.ExpandTilde(cfg.DataDir)
			running, err := server.FindRunning(root, cfg.Account)
			if err != nil {
				return err
			}
			switch len(running) {
			case 0:
				if cfg.Account != "" {
					return fmt.Errorf("no running server for account %s under %s", cfg.Account, root)
				}
				return errors.New("no running server under " + root + " (an unauthorized server holds no account lock — stop it with Ctrl-C)")
			case 1:
			default:
				ids := make([]string, 0, len(running))
				for _, r := range running {
					id := r.Account
					if id == "" {
						id = "(default)"
					}
					ids = append(ids, id)
				}
				return fmt.Errorf("several servers running under %s (%s) — select one with --account / ANY_ACCOUNT",
					root, strings.Join(ids, ", "))
			}
			r := running[0]
			if err := server.StopRunning(r, stopWait); err != nil {
				return err
			}
			return printJSON(stopResult{Stopped: true, Account: r.Account, PID: r.PID})
		},
	}
	addServerFlags(c)
	return c
}
