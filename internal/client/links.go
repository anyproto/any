package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// LinkOpts narrows a links / backlinks read: one part of the object
// (Record + Dataset, or Prop), the edge kinds to keep, and a cap.
type LinkOpts struct {
	Record  string
	Dataset string
	Prop    string
	Kinds   []string
	Limit   int
}

func (o LinkOpts) values() url.Values {
	q := url.Values{}
	if o.Record != "" {
		q.Set("record", o.Record)
	}
	if o.Dataset != "" {
		q.Set("dataset", o.Dataset)
	}
	if o.Prop != "" {
		q.Set("prop", o.Prop)
	}
	for _, k := range o.Kinds {
		q.Add("kind", k)
	}
	if o.Limit > 0 {
		q.Set("limit", fmt.Sprint(o.Limit))
	}
	return q
}

// Backlinks — GET /v1/spaces/:spaceId/objects/:objectId/backlinks.
func (c *Client) Backlinks(ctx context.Context, spaceId, objectId string, opts LinkOpts) (*api.BacklinksResponse, error) {
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/backlinks", url.PathEscape(spaceId), url.PathEscape(objectId))
	if enc := opts.values().Encode(); enc != "" {
		path += "?" + enc
	}
	var out api.BacklinksResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Links — GET /v1/spaces/:spaceId/objects/:objectId/links.
func (c *Client) Links(ctx context.Context, spaceId, objectId string, opts LinkOpts) (*api.LinksResponse, error) {
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/links", url.PathEscape(spaceId), url.PathEscape(objectId))
	if enc := opts.values().Encode(); enc != "" {
		path += "?" + enc
	}
	var out api.LinksResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BacklinksAll — GET /v1/backlinks?target=… across every indexed space.
func (c *Client) BacklinksAll(ctx context.Context, target string, kinds []string, limit int) (*api.BacklinksAllResponse, error) {
	q := LinkOpts{Kinds: kinds, Limit: limit}.values()
	q.Set("target", target)
	var out api.BacklinksAllResponse
	if err := c.do(ctx, http.MethodGet, "/v1/backlinks?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
