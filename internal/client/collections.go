package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// Collections — /v1/spaces/:spaceId/collections. A collection is a type
// without parts or layout: the column group an object is filed under
// (`any.collections`) next to its one type. The property calls are the
// type ones on a collection owner.

// CollectionsCreate creates a user-defined collection in a space.
func (c *Client) CollectionsCreate(ctx context.Context, spaceId string, req api.CollectionsCreateRequest) (*api.CollectionsCreateResponse, error) {
	var out api.CollectionsCreateResponse
	path := fmt.Sprintf("/v1/spaces/%s/collections", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CollectionsList lists the collections in a space (built-ins +
// user-defined). Hidden ones — the built-in miniapp / bin included —
// come back only with includeHidden.
func (c *Client) CollectionsList(ctx context.Context, spaceId string, includeHidden bool) (*api.CollectionsListResponse, error) {
	var out api.CollectionsListResponse
	path := fmt.Sprintf("/v1/spaces/%s/collections", url.PathEscape(spaceId))
	if includeHidden {
		path += "?includeHidden=true"
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CollectionGet reads one collection, hidden ones included.
func (c *Client) CollectionGet(ctx context.Context, spaceId, collectionId string) (*api.CollectionInfo, error) {
	var out api.CollectionInfo
	path := fmt.Sprintf("/v1/spaces/%s/collections/%s",
		url.PathEscape(spaceId), url.PathEscape(collectionId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CollectionPatch patches a collection's display and listing metadata.
// Returns nil on success (204).
func (c *Client) CollectionPatch(ctx context.Context, spaceId, collectionId string, req api.CollectionPatchRequest) error {
	path := fmt.Sprintf("/v1/spaces/%s/collections/%s",
		url.PathEscape(spaceId), url.PathEscape(collectionId))
	return c.do(ctx, http.MethodPatch, path, req, nil)
}

// CollectionListProperties returns a collection's property definitions.
func (c *Client) CollectionListProperties(ctx context.Context, spaceId, collectionId string) (*api.PropertiesListResponse, error) {
	var out api.PropertiesListResponse
	path := fmt.Sprintf("/v1/spaces/%s/collections/%s/properties",
		url.PathEscape(spaceId), url.PathEscape(collectionId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CollectionAddProperty mints a new property on a collection. Returns
// the derived propId.
func (c *Client) CollectionAddProperty(ctx context.Context, spaceId, collectionId string, req api.AddPropertyRequest) (*api.AddPropertyResponse, error) {
	var out api.AddPropertyResponse
	path := fmt.Sprintf("/v1/spaces/%s/collections/%s/properties",
		url.PathEscape(spaceId), url.PathEscape(collectionId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CollectionPatchProperty applies a {set, unset} patch to a property
// definition (rename, option CRUD, colors, order). Returns nil on
// success (204).
func (c *Client) CollectionPatchProperty(ctx context.Context, spaceId, collectionId, propId string, req api.PropertyPatchRequest) error {
	path := fmt.Sprintf("/v1/spaces/%s/collections/%s/properties/%s",
		url.PathEscape(spaceId), url.PathEscape(collectionId), url.PathEscape(propId))
	return c.do(ctx, http.MethodPatch, path, req, nil)
}

// CollectionRemoveProperty removes a property definition. Returns nil
// on success (204).
func (c *Client) CollectionRemoveProperty(ctx context.Context, spaceId, collectionId, propId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/collections/%s/properties/%s",
		url.PathEscape(spaceId), url.PathEscape(collectionId), url.PathEscape(propId))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
