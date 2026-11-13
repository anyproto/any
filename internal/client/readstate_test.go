package client

import (
	"context"
	"net/http"
	"testing"
)

// TestReadStateRoutes pins the unread feed call to its endpoint.
func TestReadStateRoutes(t *testing.T) {
	ctx := context.Background()
	checkCalls(t, []struct {
		name string
		fn   func(cl *Client) error
		want call
	}{
		{"start", func(cl *Client) error {
			_, err := cl.ReadStateUnread(ctx, "sp", "", 0)
			return err
		}, call{http.MethodGet, "/v1/spaces/sp/read-state/unread", ""}},
		{"resume", func(cl *Client) error {
			_, err := cl.ReadStateUnread(ctx, "sp", "gen.7", 50)
			return err
		}, call{http.MethodGet, "/v1/spaces/sp/read-state/unread?cursor=gen.7&limit=50", ""}},
		{"bad limit reaches the server", func(cl *Client) error {
			_, err := cl.ReadStateUnread(ctx, "sp", "", -1)
			return err
		}, call{http.MethodGet, "/v1/spaces/sp/read-state/unread?limit=-1", ""}},
	})
}
