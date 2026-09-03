package cli

import (
	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// newPushCmd: `any push ...` — this device's push-notification
// registration state (token) and the account's server-held topic
// subscriptions. All commands need a push node configured on the
// server (push.peerId / push.addrs) — otherwise 409 push.disabled.
// See docs/20-push.md.
func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "push notifications (device token, topic subscriptions)",
	}
	cmd.AddCommand(newPushTokenCmd(), newPushSubscriptionsCmd())
	return cmd
}

func newPushTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "this device's mobile push token (APNs/FCM)",
	}
	cmd.AddCommand(newPushTokenSetCmd(), newPushTokenRevokeCmd(), newPushTokenStatusCmd())
	return cmd
}

// newPushTokenSetCmd: `any push token set --platform ios|android
// --token T` — POST /v1/push/token. Persisted locally + registered
// with the push node (a slow node never fails the call; the forward
// retries in the background). Prints nothing on success.
func newPushTokenSetCmd() *cobra.Command {
	var platform, token string
	cmd := &cobra.Command{
		Use:   "set --platform ios|android --token TOKEN",
		Short: "register this device's push token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.PushTokenSet(cmd.Context(), api.PushTokenSetRequest{
				Platform: platform,
				Token:    token,
			})
		},
	}
	cmd.Flags().StringVar(&platform, "platform", "", "mobile push transport: ios | android")
	cmd.Flags().StringVar(&token, "token", "", "the opaque APNs/FCM device token")
	_ = cmd.MarkFlagRequired("platform")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

// newPushTokenRevokeCmd: `any push token revoke` — DELETE
// /v1/push/token. The local persist is removed even when the push node
// is unreachable. Prints nothing on success.
func newPushTokenRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke",
		Short: "revoke this device's push token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.PushTokenRevoke(cmd.Context())
		},
	}
}

// newPushTokenStatusCmd: `any push token status` — GET /v1/push/token,
// the LOCAL registration state (no push-node round trip).
func newPushTokenStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "this device's push-token registration state (local)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.PushTokenStatus(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// newPushSubscriptionsCmd: `any push subscriptions` — GET
// /v1/push/subscriptions, the account's topic set as the push server
// holds it. Rows are {spaceKey, topic} — spaceKey is the base58 space
// push public key (the push server's space identifier), not a spaceId.
func newPushSubscriptionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "subscriptions",
		Short: "list the account's push topic subscriptions (server-held)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.PushSubscriptions(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}
