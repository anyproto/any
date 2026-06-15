package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// globalFlags is the flat bag of flags shared across subcommands.
// CLI-only: Addr and Timeout. Server-facing: ConfigPath, DataDir,
// Addr (reused as bind addr in `run`), WalletPath, LogLevel.
type globalFlags struct {
	// CLI client
	Addr    string
	Timeout time.Duration
	Verbose bool

	// Server (used by `run` and `init`)
	ConfigPath    string
	DataDir       string
	WalletPath    string
	LogLevel      string
	PasskeyStdin  bool
}

var flags globalFlags

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

	root.AddCommand(
		newRunCmd(),
		newInitCmd(),
		newStatusCmd(),
		newStopCmd(),
		newQuerySubscribeCmd(),
		newAggregateCmd(),
		newChatCmd(),
		newAgentCmd(),
		newEditorCmd(),
		newAccountCmd(),
		newSpaceCmd(),
		newDatasetsCmd(),
		newSearchCmd(),
		newMembersCmd(),
		newInviteCmd(),
		newJoinCmd(),
		newACLCmd(),
		newDebugCmd(),
		newSyncStatusCmd(),
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
