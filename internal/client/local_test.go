package client

import (
	"context"
	"net/http"
	"testing"
)

// TestLocalCollectionsRoute pins the listing's query: counts only on
// request.
func TestLocalCollectionsRoute(t *testing.T) {
	ctx := context.Background()
	checkCalls(t, []struct {
		name string
		fn   func(cl *Client) error
		want call
	}{
		{"plain", func(cl *Client) error {
			_, err := cl.LocalCollections(ctx, "account", "", false)
			return err
		}, call{http.MethodGet, "/v1/local/collections?scope=account", ""}},
		{"count", func(cl *Client) error {
			_, err := cl.LocalCollections(ctx, "", "sp", true)
			return err
		}, call{http.MethodGet, "/v1/local/collections?count=true&spaceId=sp", ""}},
	})
}
