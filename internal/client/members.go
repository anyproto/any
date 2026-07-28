package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

func (c *Client) MembersList(ctx context.Context, spaceId string) (*api.MembersListResponse, error) {
	var out api.MembersListResponse
	path := "/v1/spaces/" + url.PathEscape(spaceId) + "/members"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) MembersMe(ctx context.Context, spaceId string) (*api.Member, error) {
	var out api.Member
	path := "/v1/spaces/" + url.PathEscape(spaceId) + "/members/me"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) MembersGet(ctx context.Context, spaceId, identity string) (*api.Member, error) {
	var out api.Member
	path := fmt.Sprintf("/v1/spaces/%s/members/%s",
		url.PathEscape(spaceId), url.PathEscape(identity))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) JoinRequests(ctx context.Context, spaceId string) (*api.JoinRequestsResponse, error) {
	var out api.JoinRequestsResponse
	path := "/v1/spaces/" + url.PathEscape(spaceId) + "/members/requests"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamSubscribeMembers opens GET /v1/spaces/:spaceId/members/subscribe
// and dispatches every SSE frame to fn. Blocks until closed or ctx canceled.
func (c *Client) StreamSubscribeMembers(ctx context.Context, spaceId string, fn func(SSEFrame) error) error {
	path := fmt.Sprintf("/v1/spaces/%s/members/subscribe", url.PathEscape(spaceId))
	return c.streamSSE(ctx, http.MethodGet, path, nil, fn)
}

// Invite mint / list / revoke ----------------------------------------------

func (c *Client) InviteCreate(ctx context.Context, spaceId string) (*api.InviteCreateResponse, error) {
	var out api.InviteCreateResponse
	path := "/v1/spaces/" + url.PathEscape(spaceId) + "/invites"
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) InvitesList(ctx context.Context, spaceId string) (*api.InvitesListResponse, error) {
	var out api.InvitesListResponse
	path := "/v1/spaces/" + url.PathEscape(spaceId) + "/invites"
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) InviteGet(ctx context.Context, spaceId, recordId string) (*api.InviteInfo, error) {
	var out api.InviteInfo
	path := fmt.Sprintf("/v1/spaces/%s/invites/%s",
		url.PathEscape(spaceId), url.PathEscape(recordId))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) InviteRevoke(ctx context.Context, spaceId, recordId string) error {
	path := fmt.Sprintf("/v1/spaces/%s/invites/%s",
		url.PathEscape(spaceId), url.PathEscape(recordId))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) InvitesRevokeAll(ctx context.Context, spaceId string) error {
	path := "/v1/spaces/" + url.PathEscape(spaceId) + "/invites"
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// Guest key (public read-only access) --------------------------------------

func (c *Client) GuestKeyCreate(ctx context.Context, spaceId string) (*api.InviteCreateResponse, error) {
	var out api.InviteCreateResponse
	path := "/v1/spaces/" + url.PathEscape(spaceId) + "/guest-key"
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GuestKeyRevoke(ctx context.Context, spaceId string) error {
	path := "/v1/spaces/" + url.PathEscape(spaceId) + "/guest-key"
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// Service.Join -------------------------------------------------------------

func (c *Client) SpaceJoin(ctx context.Context, req api.SpaceJoinRequest) (*api.SpaceInfo, error) {
	var out api.SpaceInfo
	if err := c.do(ctx, http.MethodPost, "/v1/spaces/join", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ACL operations -----------------------------------------------------------

func (c *Client) ACLAccept(ctx context.Context, spaceId string, req api.ACLAcceptRequest) error {
	return c.do(ctx, http.MethodPost,
		"/v1/spaces/"+url.PathEscape(spaceId)+"/acl/accept", req, nil)
}

func (c *Client) ACLDecline(ctx context.Context, spaceId string, req api.ACLDeclineRequest) error {
	return c.do(ctx, http.MethodPost,
		"/v1/spaces/"+url.PathEscape(spaceId)+"/acl/decline", req, nil)
}

func (c *Client) ACLChangePermissions(ctx context.Context, spaceId string, req api.ACLChangePermissionsRequest) error {
	return c.do(ctx, http.MethodPost,
		"/v1/spaces/"+url.PathEscape(spaceId)+"/acl/permissions", req, nil)
}

func (c *Client) ACLRemove(ctx context.Context, spaceId string, req api.ACLRemoveRequest) error {
	return c.do(ctx, http.MethodPost,
		"/v1/spaces/"+url.PathEscape(spaceId)+"/acl/remove", req, nil)
}

func (c *Client) ACLAdd(ctx context.Context, spaceId string, req api.ACLAddRequest) error {
	return c.do(ctx, http.MethodPost,
		"/v1/spaces/"+url.PathEscape(spaceId)+"/acl/add", req, nil)
}

func (c *Client) ACLOwnership(ctx context.Context, spaceId string, req api.ACLOwnershipRequest) error {
	return c.do(ctx, http.MethodPost,
		"/v1/spaces/"+url.PathEscape(spaceId)+"/acl/ownership", req, nil)
}

func (c *Client) ACLSelfRemove(ctx context.Context, spaceId string) error {
	return c.do(ctx, http.MethodPost,
		"/v1/spaces/"+url.PathEscape(spaceId)+"/acl/self-remove", nil, nil)
}

func (c *Client) ACLCancelJoin(ctx context.Context, spaceId string) error {
	return c.do(ctx, http.MethodPost,
		"/v1/spaces/"+url.PathEscape(spaceId)+"/acl/cancel-join", nil, nil)
}

func (c *Client) ACLStopSharing(ctx context.Context, spaceId string) error {
	return c.do(ctx, http.MethodPost,
		"/v1/spaces/"+url.PathEscape(spaceId)+"/acl/stop-sharing", nil, nil)
}
