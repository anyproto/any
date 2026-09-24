package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/anyproto/any/internal/api"
)

// `any devices ...` — the account's device registry: one tech-space
// row per device (peer) with per-app install flags and the
// active-instance claims (SYN-165). Account-scoped — not tied to any
// single space. See docs/23-devices.md for the election contract.
func newDevicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "devices",
		Short: "account device registry (per-app install flags, active-instance election)",
	}
	cmd.AddCommand(
		newDevicesListCmd(),
		newDevicesRegisterCmd(),
		newDevicesActivateCmd(),
		newDevicesRemoveCmd(),
		newDevicesQueryCmd(),
		newDevicesSubscribeCmd(),
	)
	return cmd
}

func newDevicesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list every device plus the per-app active winners",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			out, err := cl.DevicesList(cmd.Context())
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
}

// newDevicesRegisterCmd: `any devices register` — PUT /v1/devices/me.
// os/version are stamped server-side; this sets the caller-owned
// fields (display name, installed apps) on THIS device's row.
func newDevicesRegisterCmd() *cobra.Command {
	var (
		name       string
		apps       []string
		removeApps []string
	)
	cmd := &cobra.Command{
		Use:   "register",
		Short: "update this device's row (display name, installed apps)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.DeviceUpdateRequest{Name: name}
			if len(apps) > 0 || len(removeApps) > 0 {
				req.Apps = make(map[string]map[string]any, len(apps)+len(removeApps))
			}
			for _, spec := range apps {
				slug, ver, hasVer := strings.Cut(spec, "=")
				if slug == "" {
					return fmt.Errorf("--app: empty slug in %q", spec)
				}
				info := map[string]any{}
				if hasVer {
					info["version"] = ver
				}
				req.Apps[slug] = info
			}
			for _, slug := range removeApps {
				if slug == "" {
					return fmt.Errorf("--remove-app: empty slug")
				}
				req.Apps[slug] = nil // null on the wire = uninstall
			}
			cl := newClient(flags.Timeout)
			return cl.DeviceUpdateMe(cmd.Context(), req)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name for this device")
	cmd.Flags().StringArrayVar(&apps, "app", nil, "mark an app installed: slug or slug=version (repeatable)")
	cmd.Flags().StringArrayVar(&removeApps, "remove-app", nil, "mark an app uninstalled (repeatable)")
	return cmd
}

func newDevicesActivateCmd() *cobra.Command {
	var peer string
	cmd := &cobra.Command{
		Use:   "activate <app>",
		Short: "claim the active role for an app on THIS device, or on --peer",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl := newClient(flags.Timeout)
			return cl.DeviceActivate(cmd.Context(), args[0], peer)
		},
	}
	cmd.Flags().StringVar(&peer, "peer", "", "peer id of the device to hand the role to (must have the app installed)")
	return cmd
}

// newDevicesRemoveCmd: DELETE /v1/devices/:peerId. Guarded by --yes
// because the removal is permanent for that peerId: record tombstones
// are sticky, so a pruned device can never re-register without
// re-deriving peer keys (a fresh `any init`).
func newDevicesRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <peerId>",
		Short: "prune a device's row (permanent for that peer id)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("removal is permanent for this peer id (sticky tombstone) — re-run with --yes")
			}
			cl := newClient(flags.Timeout)
			return cl.DeviceDelete(cmd.Context(), args[0])
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm the permanent removal")
	return cmd
}

func newDevicesQueryCmd() *cobra.Command {
	var (
		filter     string
		sort       string
		projection string
		limit      int
		offset     int
		includeTot bool
	)
	cmd := &cobra.Command{
		Use:   "query",
		Short: "windowed snapshot over the raw devices rows (filter/sort/limit)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := buildSpaceListQueryBody("", filter, sort, projection, limit, offset, includeTot)
			if err != nil {
				return err
			}
			cl := newClient(flags.Timeout)
			out, err := cl.DevicesQuery(cmd.Context(), body)
			if err != nil {
				return err
			}
			return printJSON(out)
		},
	}
	addWindowQueryFlags(cmd, &filter, &sort, &projection, &limit, &offset, &includeTot)
	return cmd
}

// newDevicesSubscribeCmd: windowed live view over the device registry.
// Same JSON-per-line frame wrapper as `any space subscribe`.
func newDevicesSubscribeCmd() *cobra.Command {
	var (
		filter     string
		sort       string
		projection string
		limit      int
		offset     int
		includeTot bool
	)
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "open a windowed devices SSE stream (rows added/changed/removed)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := buildSpaceListQueryBody("", filter, sort, projection, limit, offset, includeTot)
			if err != nil {
				return err
			}
			cl := newClient(0) // timeout doesn't apply to streams
			return cl.StreamDevicesQuerySubscribe(cmd.Context(), body, jsonFrameHandler())
		},
	}
	addWindowQueryFlags(cmd, &filter, &sort, &projection, &limit, &offset, &includeTot)
	return cmd
}
