package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/client"
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

// newClient builds the HTTP client for a CLI call from the global
// flags. Every command goes through here so the control token rides
// each request; timeout 0 disables the request deadline (streams,
// uploads, downloads).
func newClient(timeout time.Duration) *client.Client {
	return client.New(flags.Addr, timeout).WithControlToken(flags.ControlToken)
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
	}

	root.PersistentFlags().StringVar(&flags.Addr, "addr", "", "server address (bind addr for `run`, connect addr otherwise)")
	root.PersistentFlags().DurationVar(&flags.Timeout, "timeout", 30*time.Second, "request timeout for CLI calls")
	root.PersistentFlags().BoolVar(&flags.Verbose, "verbose", false, "log HTTP request/response to stderr")
	root.PersistentFlags().StringVar(&flags.ControlToken, "control-token", os.Getenv("ANY_CONTROL_TOKEN"), "managed-mode control token (default $ANY_CONTROL_TOKEN); gates auth and shutdown on a managed server")

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
