package client

import (
	"context"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// ProcessesList calls GET /v1/processes — the live last-event-wins
// process view (expired entries swept).
func (c *Client) ProcessesList(ctx context.Context) (*api.ProcessListResponse, error) {
	var out api.ProcessListResponse
	if err := c.do(ctx, "GET", "/v1/processes", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProcessRegister calls POST /v1/processes — register a process and
// emit process.started on its scope.
func (c *Client) ProcessRegister(ctx context.Context, req api.ProcessRegisterRequest) (*api.EventPublishResponse, error) {
	var out api.EventPublishResponse
	if err := c.do(ctx, "POST", "/v1/processes", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProcessProgress calls POST /v1/processes/:id/progress — the
// progress/heartbeat frame. 404 process.not_found when the process
// isn't live under this account (register first).
func (c *Client) ProcessProgress(ctx context.Context, id string, req api.ProcessProgressRequest) (*api.EventPublishResponse, error) {
	var out api.EventPublishResponse
	if err := c.do(ctx, "POST", "/v1/processes/"+url.PathEscape(id)+"/progress", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProcessFinish calls POST /v1/processes/:id/finish — emit the
// terminal event (error required iff status failed).
func (c *Client) ProcessFinish(ctx context.Context, id string, req api.ProcessFinishRequest) (*api.EventPublishResponse, error) {
	var out api.EventPublishResponse
	if err := c.do(ctx, "POST", "/v1/processes/"+url.PathEscape(id)+"/finish", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProcessCancel calls POST /v1/processes/:id/cancel — address
// process.cancel at the owner. 404 process.not_found on no live
// match, 409 process.ambiguous when several identities share the id
// (pass req.Identity to pick one).
func (c *Client) ProcessCancel(ctx context.Context, id string, req api.ProcessCancelRequest) (*api.EventPublishResponse, error) {
	var out api.EventPublishResponse
	if err := c.do(ctx, "POST", "/v1/processes/"+url.PathEscape(id)+"/cancel", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
