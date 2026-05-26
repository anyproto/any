package server

import (
	"context"
	"net/http"
	"sync/atomic"

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

// memberMe handles GET /v1/spaces/:spaceId/members/me. Convenience over
// List for surfacing the caller's own role.
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
// Pending join requests only (a strict subset of List output).
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
// SSE stream of membership changes: new members, permission/status
// flips, and removals. Follows the sync-status callback → channel →
// streamStatusSSE pattern.
func (d *deps) subscribeMembers(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	ensureMembersWatcher(sp)

	events := make(chan space.MemberEvent, statusForwardBuffer)
	var dropped atomic.Uint64
	cancelSub := sp.Members().Subscribe(func(evt space.MemberEvent) {
		select {
		case events <- evt:
		default:
			dropped.Add(1)
		}
	})
	defer cancelSub()

	return d.streamStatusSSE(c, &dropped, func(ctx context.Context, emit func(string, any) error) error {
		for {
			select {
			case evt := <-events:
				if err := emit("member", memberEventToAPI(evt)); err != nil {
					return err
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
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
