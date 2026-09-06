package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// CatalogList reads the usecase catalog.
func (c *Client) CatalogList(ctx context.Context) (*api.CatalogListResponse, error) {
	var out api.CatalogListResponse
	if err := c.do(ctx, http.MethodGet, "/v1/catalog", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CatalogGet reads one usecase.
func (c *Client) CatalogGet(ctx context.Context, usecaseId string) (*api.CatalogUsecase, error) {
	var out api.CatalogUsecase
	if err := c.do(ctx, http.MethodGet, "/v1/catalog/"+url.PathEscape(usecaseId), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CatalogSetup sets a usecase up in a space, dependencies included.
func (c *Client) CatalogSetup(ctx context.Context, usecaseId, spaceId string) (*api.CatalogSetupResponse, error) {
	var out api.CatalogSetupResponse
	if err := c.do(ctx, http.MethodPost, "/v1/catalog/"+url.PathEscape(usecaseId)+"/setup",
		api.CatalogSetupRequest{SpaceId: spaceId}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
