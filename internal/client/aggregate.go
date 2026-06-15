package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// Aggregate runs an aggregation pipeline over a per-object dataset
// (POST /v1/spaces/:spaceId/aggregate). body is the pre-built JSON
// request — objectId, dataset, pipeline, optional limits/explain.
func (c *Client) Aggregate(ctx context.Context, spaceId string, body []byte) (*api.AggregateResponse, error) {
	path := fmt.Sprintf("/v1/spaces/%s/aggregate", url.PathEscape(spaceId))
	return c.postAggregate(ctx, path, body)
}

// AggregateObjects runs an aggregation pipeline over the per-space
// `objects` collection (POST /v1/spaces/:spaceId/objects/aggregate).
func (c *Client) AggregateObjects(ctx context.Context, spaceId string, body []byte) (*api.AggregateResponse, error) {
	path := fmt.Sprintf("/v1/spaces/%s/objects/aggregate", url.PathEscape(spaceId))
	return c.postAggregate(ctx, path, body)
}

func (c *Client) postAggregate(ctx context.Context, path string, body []byte) (*api.AggregateResponse, error) {
	var out api.AggregateResponse
	// json.RawMessage round-trips through do's json.Marshal unchanged,
	// so the pre-built body ships verbatim.
	if err := c.do(ctx, http.MethodPost, path, json.RawMessage(body), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
