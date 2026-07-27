package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// ensureMembersWatcher starts the per-space members watcher if it
// hasn't been already. The watcher is what fetches identityRepo
// profiles (name / description / iconCid); without it, applyProfile
// is a no-op and Members.Me/List/Get return raw ACL records with no
// profile overlay. The SDK only starts the watcher lazily on
// Subscribe / Query calls, so any HTTP route that wants profile data
// has to nudge it explicitly. ensureWatcher is idempotent — second
// and later calls return the running watcher in O(1).
func ensureMembersWatcher(sp space.Space) {
	_ = sp.Members().Query()
}

// memberList handles GET /v1/spaces/:spaceId/members.
//
//	@Summary	List space members
//	@Tags		members
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200		{object}	api.MembersListResponse
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/members [get]
func (d *deps) memberList(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	ensureMembersWatcher(sp)
	members, err := sp.Members().List(c.Request().Context())
	if err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	out := make([]api.Member, 0, len(members))
	for _, m := range members {
		out = append(out, memberToAPI(m))
	}
	return c.JSON(http.StatusOK, api.MembersListResponse{Members: out})
}

// memberMe handles GET /v1/spaces/:spaceId/members/me.
//
//	@Summary	Get own membership
//	@Tags		members
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200		{object}	api.Member
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/members/me [get]
func (d *deps) memberMe(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	ensureMembersWatcher(sp)
	me, err := sp.Members().Me(c.Request().Context())
	if err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	return c.JSON(http.StatusOK, memberToAPI(me))
}

// memberJoinRequests handles GET /v1/spaces/:spaceId/members/requests.
//
//	@Summary	List pending join requests
//	@Tags		members
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Success	200		{object}	api.JoinRequestsResponse
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/members/requests [get]
func (d *deps) memberJoinRequests(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	reqs, err := sp.Members().JoinRequests(c.Request().Context())
	if err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	out := make([]api.JoinRequest, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, api.JoinRequest{
			RecordId:    r.RecordId,
			Identity:    r.Identity,
			Name:        r.Name,
			Description: r.Description,
			IconCID:     r.IconCID,
		})
	}
	return c.JSON(http.StatusOK, api.JoinRequestsResponse{Requests: out})
}

// memberGet handles GET /v1/spaces/:spaceId/members/:identity.
//
//	@Summary	Get a member by identity
//	@Tags		members
//	@Produce	json
//	@Param		spaceId		path		string	true	"Space ID"
//	@Param		identity	path		string	true	"Member identity"
//	@Success	200			{object}	api.Member
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/members/{identity} [get]
func (d *deps) memberGet(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	identity := c.Param("identity")
	if identity == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "identity required", nil)
	}
	ensureMembersWatcher(sp)
	m, err := sp.Members().Get(c.Request().Context(), identity)
	if err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id(), "identity": identity})
	}
	return c.JSON(http.StatusOK, memberToAPI(m))
}

// subscribeMembers handles GET /v1/spaces/:spaceId/members/subscribe.
//
//	@Summary	Subscribe to membership changes (SSE)
//	@Tags		members
//	@Produce	text/event-stream
//	@Param		spaceId	path	string	true	"Space ID"
//	@Success	200
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/members/subscribe [get]
func (d *deps) subscribeMembers(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	ensureMembersWatcher(sp)

	return forwardSSE(d, c, "member", sp.Members().Subscribe,
		func(evt space.MemberEvent) any { return memberEventToAPI(evt) })
}

func memberEventKindString(k space.MemberEventKind) string {
	switch k {
	case space.MemberEventAdded:
		return api.MemberEventKindAdded
	case space.MemberEventChanged:
		return api.MemberEventKindChanged
	case space.MemberEventRemoved:
		return api.MemberEventKindRemoved
	default:
		return "unknown"
	}
}

func memberEventToAPI(evt space.MemberEvent) api.MemberEventPayload {
	out := api.MemberEventPayload{
		Kind:   memberEventKindString(evt.Kind),
		Member: memberToAPI(evt.Member),
	}
	if evt.Previous != nil {
		prev := memberToAPI(*evt.Previous)
		out.Previous = &prev
	}
	return out
}
