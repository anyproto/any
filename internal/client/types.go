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

// TypeDatasets lists a type's runtime dataset definitions.
func (c *Client) TypeDatasets(ctx context.Context, spaceId, typeId string) (*api.TypeDatasetsListResponse, error) {
	var out api.TypeDatasetsListResponse
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/datasets",
		url.PathEscape(spaceId), url.PathEscape(typeId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TypePatch patches a type's display and rendering metadata. Returns
// nil on success (204).
func (c *Client) TypePatch(ctx context.Context, spaceId, typeId string, req api.TypePatchRequest) error {
	path := fmt.Sprintf("/v1/spaces/%s/types/%s", url.PathEscape(spaceId), url.PathEscape(typeId))
	return c.do(ctx, http.MethodPatch, path, req, nil)
}

// TypeParts lists a type's parts with their datasets.
func (c *Client) TypeParts(ctx context.Context, spaceId, typeId string) (*api.TypePartsListResponse, error) {
	var out api.TypePartsListResponse
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/parts", url.PathEscape(spaceId), url.PathEscape(typeId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TypeAddPart declares a part (with its datasets) on a type. Returns
// the part id.
func (c *Client) TypeAddPart(ctx context.Context, spaceId, typeId string, req api.PartDraftRequest) (*api.AddPartResponse, error) {
	var out api.AddPartResponse
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/parts", url.PathEscape(spaceId), url.PathEscape(typeId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TypePatchPart applies a {set, unset} patch over a part's display
// slice. Returns nil on success (204).
func (c *Client) TypePatchPart(ctx context.Context, spaceId, typeId, partId string, req api.PartPatchRequest) error {
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/parts/%s",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(partId))
	return c.do(ctx, http.MethodPatch, path, req, nil)
}

// TypeRemovePart removes a part and its datasets (record data stays).
func (c *Client) TypeRemovePart(ctx context.Context, spaceId, typeId, partId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/parts/%s",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(partId))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// TypeAddDataset declares a dataset on a part. Returns the definition
// id and the computed collection.
func (c *Client) TypeAddDataset(ctx context.Context, spaceId, typeId, partId string, req api.DatasetDraftRequest) (*api.AddDatasetResponse, error) {
	var out api.AddDatasetResponse
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/parts/%s/datasets",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(partId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TypeAddDatasetField appends a field to a dataset definition. Returns
// the field definition id.
func (c *Client) TypeAddDatasetField(ctx context.Context, spaceId, typeId, defId string, req api.DatasetFieldDraft) (*api.AddDatasetFieldResponse, error) {
	var out api.AddDatasetFieldResponse
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/datasets/%s/fields",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(defId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TypePatchDataset applies a {set, unset} patch over a dataset
// definition's mutable leaves. Returns nil on success (204).
func (c *Client) TypePatchDataset(ctx context.Context, spaceId, typeId, defId string, req api.DatasetPatchRequest) error {
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/datasets/%s",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(defId))
	return c.do(ctx, http.MethodPatch, path, req, nil)
}

// TypePatchDatasetField applies a {set, unset} patch over one field
// definition's mutable leaves (name, description, xFormat.*). Returns
// nil on success (204).
func (c *Client) TypePatchDatasetField(ctx context.Context, spaceId, typeId, defId, fieldId string, req api.DatasetFieldPatchRequest) error {
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/datasets/%s/fields/%s",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(defId), url.PathEscape(fieldId))
	return c.do(ctx, http.MethodPatch, path, req, nil)
}

// TypeRemoveDataset removes a dataset definition (record data stays).
func (c *Client) TypeRemoveDataset(ctx context.Context, spaceId, typeId, defId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/datasets/%s",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(defId))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// TypeRemoveDatasetField removes one field definition.
func (c *Client) TypeRemoveDatasetField(ctx context.Context, spaceId, typeId, defId, fieldId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/types/%s/datasets/%s/fields/%s",
		url.PathEscape(spaceId), url.PathEscape(typeId), url.PathEscape(defId), url.PathEscape(fieldId))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// Upsert batch-ingests records into an id:user dataset.
func (c *Client) Upsert(ctx context.Context, spaceId string, req api.UpsertRequest) (*api.UpsertResult, error) {
	var out api.UpsertResult
	path := fmt.Sprintf("/v1/spaces/%s/upsert", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
