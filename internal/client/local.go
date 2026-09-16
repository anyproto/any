package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/anyproto/any/internal/api"
)

// Local store (/v1/local): device-local, non-CRDT any-store collections.
// One method per endpoint; bodies are the api.Local* request structs.

// LocalMeta — GET /v1/local/meta.
func (c *Client) LocalMeta(ctx context.Context) (*api.LocalMetaResponse, error) {
	var out api.LocalMetaResponse
	if err := c.do(ctx, http.MethodGet, "/v1/local/meta", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalCollections — GET /v1/local/collections. Empty scope lists both
// scopes; a spaceId alone implies scope=space.
func (c *Client) LocalCollections(ctx context.Context, scope, spaceId string) (*api.LocalListResponse, error) {
	q := url.Values{}
	if scope != "" {
		q.Set("scope", scope)
	}
	if spaceId != "" {
		q.Set("spaceId", spaceId)
	}
	path := "/v1/local/collections"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var out api.LocalListResponse
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalEnsure — PUT /v1/local/collections (idempotent).
func (c *Client) LocalEnsure(ctx context.Context, req api.LocalEnsureRequest) (*api.LocalEnsureResponse, error) {
	var out api.LocalEnsureResponse
	if err := c.do(ctx, http.MethodPut, "/v1/local/collections", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalDrop — DELETE /v1/local/collections.
func (c *Client) LocalDrop(ctx context.Context, coll api.LocalCollection) error {
	q := url.Values{}
	q.Set("scope", coll.Scope)
	q.Set("name", coll.Name)
	if coll.SpaceId != "" {
		q.Set("spaceId", coll.SpaceId)
	}
	return c.do(ctx, http.MethodDelete, "/v1/local/collections?"+q.Encode(), nil, nil)
}

// LocalInsert — POST /v1/local/insert.
func (c *Client) LocalInsert(ctx context.Context, req api.LocalDocsRequest) (*api.LocalIdsResponse, error) {
	var out api.LocalIdsResponse
	if err := c.do(ctx, http.MethodPost, "/v1/local/insert", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalUpsert — POST /v1/local/upsert.
func (c *Client) LocalUpsert(ctx context.Context, req api.LocalDocsRequest) (*api.LocalIdsResponse, error) {
	var out api.LocalIdsResponse
	if err := c.do(ctx, http.MethodPost, "/v1/local/upsert", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalUpdate — POST /v1/local/update.
func (c *Client) LocalUpdate(ctx context.Context, req api.LocalUpdateRequest) (*api.LocalUpdateResponse, error) {
	var out api.LocalUpdateResponse
	if err := c.do(ctx, http.MethodPost, "/v1/local/update", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalDelete — POST /v1/local/delete.
func (c *Client) LocalDelete(ctx context.Context, req api.LocalDeleteRequest) (*api.LocalDeleteResponse, error) {
	var out api.LocalDeleteResponse
	if err := c.do(ctx, http.MethodPost, "/v1/local/delete", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalGet — POST /v1/local/get.
func (c *Client) LocalGet(ctx context.Context, req api.LocalGetRequest) (*api.LocalRecordResponse, error) {
	var out api.LocalRecordResponse
	if err := c.do(ctx, http.MethodPost, "/v1/local/get", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalQuery — POST /v1/local/query.
func (c *Client) LocalQuery(ctx context.Context, req api.LocalQueryRequest) (*api.QueryResponse, error) {
	var out api.QueryResponse
	if err := c.do(ctx, http.MethodPost, "/v1/local/query", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalAggregate — POST /v1/local/aggregate.
func (c *Client) LocalAggregate(ctx context.Context, req api.LocalAggregateRequest) (*api.LocalAggregateResponse, error) {
	var out api.LocalAggregateResponse
	if err := c.do(ctx, http.MethodPost, "/v1/local/aggregate", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalIndexes — POST /v1/local/indexes.
func (c *Client) LocalIndexes(ctx context.Context, req api.LocalIndexesRequest) (*api.LocalIndexesResponse, error) {
	var out api.LocalIndexesResponse
	if err := c.do(ctx, http.MethodPost, "/v1/local/indexes", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocalExport — GET /v1/local/export: the named collections as one
// gzip'd anyenc stream.
// The caller streams the body and MUST close it. names nil with a
// scope exports every collection in that scope; both empty, the whole
// local store.
func (c *Client) LocalExport(ctx context.Context, scope, spaceId string, names []string) (io.ReadCloser, error) {
	q := url.Values{}
	if scope != "" {
		q.Set("scope", scope)
	}
	if spaceId != "" {
		q.Set("spaceId", spaceId)
	}
	if len(names) > 0 {
		q.Set("names", strings.Join(names, ","))
	}
	path := "/v1/local/export"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	req, err := c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return nil, &TransportError{Addr: c.base, Err: err}
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, parseServerError(resp)
	}
	return resp.Body, nil
}

// LocalImport — POST /v1/local/import: r is an export file, streamed
// as the raw body.
func (c *Client) LocalImport(ctx context.Context, r io.Reader) (*api.LocalImportResponse, error) {
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/local/import", r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := streamHTTP.Do(req)
	if err != nil {
		return nil, &TransportError{Addr: c.base, Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, parseServerError(resp)
	}
	var out api.LocalImportResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode import reply: %w", err)
	}
	return &out, nil
}
