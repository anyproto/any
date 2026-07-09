package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// TypesCreate creates a user-defined type in a space.
func (c *Client) TypesCreate(ctx context.Context, spaceId string, req api.TypesCreateRequest) (*api.TypesCreateResponse, error) {
	var out api.TypesCreateResponse
	path := fmt.Sprintf("/v1/spaces/%s/types", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TypesList lists the types in a space (built-ins + user-defined).
func (c *Client) TypesList(ctx context.Context, spaceId string) (*api.TypesListResponse, error) {
	var out api.TypesListResponse
	path := fmt.Sprintf("/v1/spaces/%s/types", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TypeListProperties returns a type's property definitions.
func (c *Client) TypeListProperties(ctx context.Context, spaceId, typeId string) (*api.PropertiesListResponse, error) {
	var out api.PropertiesListResponse
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/properties",
		url.PathEscape(spaceId), url.PathEscape(typeId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TypeAddProperty mints a new property on a type. Returns the derived propId.
func (c *Client) TypeAddProperty(ctx context.Context, spaceId, typeId string, req api.AddPropertyRequest) (*api.AddPropertyResponse, error) {
	var out api.AddPropertyResponse
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/properties",
		url.PathEscape(spaceId), url.PathEscape(typeId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TypePatchProperty applies a {set, unset} patch to a property definition
// (rename, option CRUD, colors, order). Returns nil on success (204).
func (c *Client) TypePatchProperty(ctx context.Context, spaceId, typeId, propId string, req api.PropertyPatchRequest) error {
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/properties/%s",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(propId))
	return c.do(ctx, http.MethodPatch, path, req, nil)
}

// TypeRemoveProperty removes a property definition. Returns nil on success (204).
func (c *Client) TypeRemoveProperty(ctx context.Context, spaceId, typeId, propId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/properties/%s",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(propId))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
