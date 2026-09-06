package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/server"
)

// globalFlags is the flat bag of flags shared across subcommands.
// CLI-only: Addr and Timeout. Server-facing: ConfigPath, DataDir,
// Addr (reused as bind addr in `run`), WalletPath, LogLevel.
type globalFlags struct {
	// CLI client
	Addr    string
	Timeout time.Duration
	Verbose bool
	// ControlToken is the managed-mode control token (--control-token
	// / ANY_CONTROL_TOKEN), sent on every request; a standalone server
	// ignores it.
	ControlToken string

	// Server (used by `run` and `init`)
	ConfigPath   string
	DataDir      string
	Mode         string
	Account      string
	WalletPath   string
	LogLevel     string
	PasskeyStdin bool
}

var flags globalFlags

// topLevelName is the name of cmd's top-level subcommand (`auth` for
// `any auth login`).
func topLevelName(cmd *cobra.Command) string {
	for cmd.HasParent() && cmd.Parent().HasParent() {
		cmd = cmd.Parent()
	}
	return cmd.Name()
}

// discoverAddr returns the bound address of the one server serving the
// data dir's account (config file / ANY_DATA_DIR / ANY_ACCOUNT), or ""
// when there is none, several, or the address is unknown — the caller
// then falls back to the default. Every failure is silent: discovery
// is a convenience, not a gate.
func discoverAddr() string {
	cfg, err := config.LoadClient(config.Flags{})
	if err != nil {
		return ""
	}
	running, err := server.FindRunning(config.ExpandTilde(cfg.DataDir), cfg.Account)
	if err != nil || len(running) != 1 {
		return ""
	}
	return running[0].Addr
}

// newClient builds the HTTP client for a CLI call from the global
// flags. Every command goes through here so the control token rides
// each request; timeout 0 disables the request deadline (streams,
// uploads, downloads).
func newClient(timeout time.Duration) *client.Client {
	return client.New(flags.Addr, timeout).WithControlToken(controlToken())
}

// controlToken resolves the managed-mode control token: the flag, else
// the environment. Read here rather than as the flag's default so the
// token never appears in `--help` output.
func controlToken() string {
	if flags.ControlToken != "" {
		return flags.ControlToken
	}
	return os.Getenv("ANY_CONTROL_TOKEN")
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "any",
		Short: "any — all-in-one binary wrapping any-sync-sdk over HTTP",
		Long: `any is a single binary that provides an HTTP/JSON server over
the any-sync-sdk plus a CLI client. Run 'any run' to start the server,
then any other 'any <cmd>' makes HTTP calls to it.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Without --addr a client command reaches the server serving
		// the data dir's account, wherever it bound (an ephemeral port
		// included): its recorded server.addr wins over the fixed
		// default. Server-side and lock-based commands are exempt —
		// `run` binds the flag, `init` and `stop` never connect.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			if flags.Addr != "" {
				return
			}
			switch topLevelName(cmd) {
			case "run", "init", "stop":
				return
			}
			if addr := discoverAddr(); addr != "" {
				flags.Addr = addr
			}
		},
	}

	root.PersistentFlags().StringVar(&flags.Addr, "addr", "", "server address (bind addr for `run`, connect addr otherwise)")
	root.PersistentFlags().DurationVar(&flags.Timeout, "timeout", 30*time.Second, "request timeout for CLI calls")
	root.PersistentFlags().BoolVar(&flags.Verbose, "verbose", false, "log HTTP request/response to stderr")
	root.PersistentFlags().StringVar(&flags.ControlToken, "control-token", "", "managed-mode control token; prefer ANY_CONTROL_TOKEN (a flag value is visible in the process list). Gates auth and shutdown on a managed server")

	root.AddCommand(
		newRunCmd(),
		newInitCmd(),
		newAuthCmd(),
		newStatusCmd(),
		newStopCmd(),
		newQuerySubscribeCmd(),
		newAggregateCmd(),
		newUpsertCmd(),
		newTypeCmd(),
		newBundleCmd(),
		newCatalogCmd(),
		newChatCmd(),
		newEditorCmd(),
		newFileCmd(),
		newAccountCmd(),
		newIdentitiesCmd(),
		newDevicesCmd(),
		newLocalCmd(),
		newSpaceCmd(),
		newDatasetsCmd(),
		newSearchCmd(),
		newMembersCmd(),
		newInviteCmd(),
		newJoinCmd(),
		newOneToOneCmd(),
		newACLCmd(),
		newDebugCmd(),
		newSyncStatusCmd(),
		newEventsCmd(),
		newProcessCmd(),
		newPushCmd(),
		newVersionCmd(),
	)
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	root := newRootCmd()
	err := root.Execute()
	if err == nil {
		return 0
	}
	fmt.Fprintln(os.Stderr, "any:", err)
	return exitCode(err)
}
