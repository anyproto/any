package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// Objects — create, plus the two bindings that say what an object is
// and where it is filed: its one type (`any.type`) and the collections
// it belongs to (`any.collections`). Both bindings live under the
// server's /properties/:objectId routes.

// ObjectsCreate creates an object from its type, the collections it is
// filed under and its initial property values. Returns the new object
// id.
func (c *Client) ObjectsCreate(ctx context.Context, spaceId string, req api.ObjectCreateRequest) (*api.ObjectsCreateResponse, error) {
	var out api.ObjectsCreateResponse
	path := fmt.Sprintf("/v1/spaces/%s/objects", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ObjectSetType sets the object's one type, replacing any previous one
// (its values stay as orphan data). The object must already exist.
func (c *Client) ObjectSetType(ctx context.Context, spaceId, objectId, typeId string) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/properties/%s/type/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId), url.PathEscape(typeId))
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ObjectAttachCollection files the object under a collection
// (idempotent), admitting writes to its columns. The `bin` collection
// is move to bin.
func (c *Client) ObjectAttachCollection(ctx context.Context, spaceId, objectId, collectionId string) (*api.ModifyResult, error) {
	var out api.ModifyResult
	if err := c.do(ctx, http.MethodPost, objectCollectionPath(spaceId, objectId, collectionId), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ObjectDetachCollection removes the object from a collection
// (idempotent); values in that namespace stay as orphan data.
// Detaching `bin` is restore from the bin.
func (c *Client) ObjectDetachCollection(ctx context.Context, spaceId, objectId, collectionId string) (*api.ModifyResult, error) {
	var out api.ModifyResult
	if err := c.do(ctx, http.MethodDelete, objectCollectionPath(spaceId, objectId, collectionId), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func objectCollectionPath(spaceId, objectId, collectionId string) string {
	return fmt.Sprintf("/v1/spaces/%s/properties/%s/collections/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId), url.PathEscape(collectionId))
}
