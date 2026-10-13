package cli

import (
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// `any account ...` — account-level operations that aren't scoped to
// any single space. Mirrors sdk.Account() in the SDK.
func newAccountCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "account-level operations (profile, identity)",
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

	var iconName, iconMime string
	setIcon := &cobra.Command{
		Use:   "set-icon <path>",
		Short: "upload the avatar image (PUT /v1/account/icon)",
		Long: `Uploads the image at <path> (- for stdin) as the raw body of
PUT /v1/account/icon and prints the profile with the new iconCid. The
media type is resolved by the server from the content unless --mime
overrides it; the avatar reaches contacts once its network backup is
confirmed.`,
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
			out, err := cl.AccountSetIcon(cmd.Context(), in, iconName, iconMime)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	setIcon.Flags().StringVar(&iconName, "name", "", "user-facing file name (default: basename of <path>)")
	setIcon.Flags().StringVar(&iconMime, "mime", "", "content type (default: resolved by the server from the content)")

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

	cmd.AddCommand(setMeta, setIcon, icon, deleteIcon, redeem)
	return cmd
}
