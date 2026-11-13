package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/anyproto/any/internal/api"
)

// ReadStateUnread lists the changes that became unread on this device
// after cursor ("" starts at the present). limit 0 takes the server's
// default page size.
func (c *Client) ReadStateUnread(ctx context.Context, spaceId, cursor string, limit int) (*api.UnreadResponse, error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit != 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := fmt.Sprintf("/v1/spaces/%s/read-state/unread", url.PathEscape(spaceId))
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out api.UnreadResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
