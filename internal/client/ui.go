package client

import (
	"context"
	"net/http"

	"github.com/anyproto/any/internal/api"
)

// UICommandPublish posts one command to the account-wide UI command
// channel (POST /v1/ui/commands). Returns how many connected UI windows
// received it (0 = nobody listening; still a success). See
// docs/15-ui-commands.md.
func (c *Client) UICommandPublish(ctx context.Context, cmd api.UICommand) (*api.UICommandPublishResponse, error) {
	var out api.UICommandPublishResponse
	if err := c.do(ctx, http.MethodPost, "/v1/ui/commands", cmd, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamUICommands opens GET /v1/ui/commands/subscribe and dispatches
// every SSE frame to fn. Blocks until a `closed` frame, EOF, or ctx
// cancel. There is no snapshot — only commands published after connect
// are delivered.
func (c *Client) StreamUICommands(ctx context.Context, fn func(SSEFrame) error) error {
	return c.streamSSE(ctx, http.MethodGet, "/v1/ui/commands/subscribe", nil, fn)
}
