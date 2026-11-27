package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// deletionCallTimeout is the request timeout of a deletion request or
// revert: --timeout, but never under the time the server gives the
// network, so the server's answer is the one the caller reads.
func deletionCallTimeout() time.Duration {
	const serverWait = 45 * time.Second
	return max(flags.Timeout, serverWait)
}

// `any account ...` — account-level operations that aren't scoped to
// any single space. Mirrors sdk.Account() in the SDK.
func newAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "the account (GET /v1/account) and account-level operations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			res, err := cl.Account(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(res)
		},
	}

	var name, desc string
	setMeta := &cobra.Command{
		Use:   "set-metadata",
		Short: "publish profile (name / description) across every space",
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.AccountUpdateMetadata(cmd.Context(), api.AccountMetadata{
				Name:        name,
				Description: desc,
			})
		},
	}
	setMeta.Flags().StringVar(&name, "name", "", "display name")
	setMeta.Flags().StringVar(&desc, "description", "", "short bio / description")

	var iconName string
	setIcon := &cobra.Command{
		Use:   "set-icon <path>",
		Short: "upload the avatar image (PUT /v1/account/icon)",
		Long: `Uploads the image at <path> (- for stdin) as the raw body of
PUT /v1/account/icon and prints the profile with the new iconCid. The
server reads the image type from the bytes (PNG, JPEG, GIF, WebP and
other raster formats; SVG is refused); the avatar reaches contacts
once its network backup is confirmed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			var in io.Reader
			if path == "-" {
				in = os.Stdin
			} else {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()
				in = f
				if iconName == "" {
					iconName = filepath.Base(path)
				}
			}
			cl := newClient(0) // uploads must not hit the request timeout
			out, err := cl.AccountSetIcon(cmd.Context(), in, iconName)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	setIcon.Flags().StringVar(&iconName, "name", "", "user-facing file name (default: basename of <path>)")

	var output string
	icon := &cobra.Command{
		Use:   "icon",
		Short: "download the avatar (stdout by default, -o for a file)",
		Long: `Streams GET /v1/account/icon. Without -o the raw bytes go to stdout;
with -o PATH they are written to PATH and a small JSON receipt is
printed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(0)
			res, err := cl.AccountIcon(cmd.Context())
			if err != nil {
				return err
			}
			defer res.Body.Close()
			return writeDownload(output, res)
		},
	}
	icon.Flags().StringVarP(&output, "output", "o", "", "write the image to this path (default: stdout)")

	deleteIcon := &cobra.Command{
		Use:   "delete-icon",
		Short: "remove the avatar (DELETE /v1/account/icon)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.AccountDeleteIcon(cmd.Context())
		},
	}

	redeem := &cobra.Command{
		Use:   "redeem <code>",
		Short: "redeem an alpha invite code to raise this account's limits",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			res, err := cl.AccountRedeemAccessCode(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return printJSON(res)
		},
	}

	limits := &cobra.Command{
		Use:   "limits",
		Short: "the network's storage pool and shared-spaces limit (GET /v1/account/limits)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			res, err := cl.AccountLimits(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(res)
		},
	}

	var yes bool
	deleteAccount := &cobra.Command{
		Use:   "delete",
		Short: "request deletion of the account from the network (requires --yes)",
		Long: `Sends POST /v1/account/deletion and prints the state with the date the
network deletes the account's data. Until then the network may stop
syncing the spaces the account owns; "any account revert-deletion"
cancels the request while it is pending. Nothing changes on this device.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to request the account's deletion without --yes")
			}
			cl := newClient(deletionCallTimeout())
			res, err := cl.AccountRequestDeletion(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(res)
		},
	}
	deleteAccount.Flags().BoolVar(&yes, "yes", false, "confirm the deletion request")

	revertDeletion := &cobra.Command{
		Use:   "revert-deletion",
		Short: "cancel a pending deletion of the account (DELETE /v1/account/deletion)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(deletionCallTimeout())
			res, err := cl.AccountRevertDeletion(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(res)
		},
	}

	cmd.AddCommand(setMeta, setIcon, icon, deleteIcon, redeem, limits, deleteAccount, revertDeletion)
	return cmd
}
