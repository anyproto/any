package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// BlocksCreate posts a new block. The server allocates the new
// block's id (auto-derived from the change CID) and, when nav.pos is
// empty in the request, fills in the next pos after the parent's
// current max.
func (c *Client) BlocksCreate(ctx context.Context, spaceId, objectId string, req api.BlockCreateRequest) (*api.Block, error) {
	var out api.Block
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/editor/blocks",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BlocksPatch applies a {set, unset} patch atomically to one block.
// Empty patch returns the current versionId without writing.
func (c *Client) BlocksPatch(ctx context.Context, spaceId, objectId, blockId string, req api.BlockPatchRequest) (*api.BlockPatchResponse, error) {
	var out api.BlockPatchResponse
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/editor/blocks/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId), url.PathEscape(blockId))
	if err := c.do(ctx, http.MethodPatch, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// BlocksDelete tombstones one block. Children of the deleted block
// are NOT cascaded.
func (c *Client) BlocksDelete(ctx context.Context, spaceId, objectId, blockId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/editor/blocks/%s",
		url.PathEscape(spaceId), url.PathEscape(objectId), url.PathEscape(blockId))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
