package client

import (
	"context"
	"testing"
)

// TestChatReadRoutes pins the chat read calls to their endpoints.
func TestChatReadRoutes(t *testing.T) {
	ctx := context.Background()
	checkCalls(t, []struct {
		name string
		fn   func(cl *Client) error
		want call
	}{
		{"read", func(cl *Client) error { return cl.ChatRead(ctx, "sp", "obj", "m1") },
			call{"POST", "/v1/spaces/sp/objects/obj/chat/messages/m1/read", ""}},
		{"read-all", func(cl *Client) error { return cl.ChatReadAll(ctx, "sp", "obj") },
			call{"POST", "/v1/spaces/sp/objects/obj/chat/read-all", ""}},
		{"reactions-read", func(cl *Client) error { return cl.ChatReactionsRead(ctx, "sp", "obj", "m1") },
			call{"POST", "/v1/spaces/sp/objects/obj/chat/messages/m1/reactions-read", ""}},
	})
}
