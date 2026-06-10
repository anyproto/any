package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// Search runs a local-index search in the space (FTS / vector / hybrid;
// see docs/11-index.md).
func (c *Client) Search(ctx context.Context, spaceId string, req api.SearchRequest) (*api.SearchResponse, error) {
	var out api.SearchResponse
	path := fmt.Sprintf("/v1/spaces/%s/search", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
