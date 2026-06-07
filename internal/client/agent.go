package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// AgentTurnAppend appends one immutable turn record to the chat
// object's agent_turns dataset. recordIds[0] is the zero-padded-seq
// record id; read back via /query with dataset=agent_turns.
func (c *Client) AgentTurnAppend(ctx context.Context, spaceId, objectId string, req api.AgentTurnAppendRequest) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/agent/turns",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AgentChunkCreate writes one immutable compressed-history chunk to
// the chat object's agent_chunks dataset.
func (c *Client) AgentChunkCreate(ctx context.Context, spaceId, objectId string, req api.AgentChunkCreateRequest) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/objects/%s/agent/chunks",
		url.PathEscape(spaceId), url.PathEscape(objectId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AgentBrain resolves the deterministic per-space brain object id that
// hosts the agent_memory_items dataset.
func (c *Client) AgentBrain(ctx context.Context, spaceId string) (*api.AgentBrainResponse, error) {
	var out api.AgentBrainResponse
	path := fmt.Sprintf("/v1/spaces/%s/agent/brain", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AgentMemoryCreate creates one memory item on the space brain object.
// recordIds[0] is the derived item id.
func (c *Client) AgentMemoryCreate(ctx context.Context, spaceId string, req api.AgentMemoryCreateRequest) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/agent/memory", url.PathEscape(spaceId))
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AgentMemoryEvolve patches a memory item's mutable fields (author
// only).
func (c *Client) AgentMemoryEvolve(ctx context.Context, spaceId, itemId string, req api.AgentMemoryEvolveRequest) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/agent/memory/%s",
		url.PathEscape(spaceId), url.PathEscape(itemId))
	if err := c.do(ctx, http.MethodPatch, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AgentMemoryDelete tombstones a memory item (author only).
func (c *Client) AgentMemoryDelete(ctx context.Context, spaceId, itemId string) (*api.ModifyResult, error) {
	var out api.ModifyResult
	path := fmt.Sprintf("/v1/spaces/%s/agent/memory/%s",
		url.PathEscape(spaceId), url.PathEscape(itemId))
	if err := c.do(ctx, http.MethodDelete, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
