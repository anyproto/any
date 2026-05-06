package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// memberList handles GET /v1/spaces/:spaceId/members.
func (d *deps) memberList(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
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
	m, err := sp.Members().Get(c.Request().Context(), identity)
	if err != nil {
		return aclOpError(c, err, map[string]any{"spaceId": sp.Id(), "identity": identity})
	}
	return c.JSON(http.StatusOK, memberToAPI(m))
}
