package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// EventsPublish posts one event to the account-wide event bus
// (POST /v1/events). Returns how many local subscribers matched it
// (0 = nobody listening; still a success). See docs/21-events.md.
func (c *Client) EventsPublish(ctx context.Context, req api.EventPublishRequest) (*api.EventPublishResponse, error) {
	var out api.EventPublishResponse
	if err := c.do(ctx, http.MethodPost, "/v1/events", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamEvents opens GET /v1/events/subscribe with the given filter
// params (repeatable keys scope / spaceId / type / target; nil = no
// filter) and dispatches every SSE frame to fn. Blocks until a
// `closed` frame, EOF, or ctx cancel. There is no snapshot — only
// events published after connect are delivered.
func (c *Client) StreamEvents(ctx context.Context, filter url.Values, fn func(SSEFrame) error) error {
	path := "/v1/events/subscribe"
	if len(filter) > 0 {
		path += "?" + filter.Encode()
	}
	return c.streamSSE(ctx, http.MethodGet, path, nil, fn)
}
