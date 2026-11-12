package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

const (
	readStateDefaultLimit = 100
	readStateMaxLimit     = 1000
)

// readStateUnread handles GET /v1/spaces/:spaceId/read-state/unread.
//
//	@Summary	List the changes that became unread on this device after a cursor
//	@Tags		readstate
//	@Produce	json
//	@Param		spaceId	path		string	true	"Space ID"
//	@Param		cursor	query		string	false	"Cursor from the previous call; omit to start at the present"
//	@Param		limit	query		int		false	"Page size (default 100, max 1000)"
//	@Success	200		{object}	api.UnreadResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/spaces/{spaceId}/read-state/unread [get]
func (d *deps) readStateUnread(c echo.Context) error {
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return errResp
	}
	cur, err := parseReadCursor(c.QueryParam("cursor"))
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"cursor is malformed", nil)
	}
	limit, ok := parseReadLimit(c.QueryParam("limit"))
	if !ok {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"limit must be a positive integer", nil)
	}

	changes, next, err := sp.ReadState().UnreadSince(c.Request().Context(), cur, limit)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"spaceId": sp.Id()})
	}
	out := api.UnreadResponse{
		Changes: make([]api.UnreadChange, 0, len(changes)),
		Cursor:  formatReadCursor(next),
	}
	for _, ch := range changes {
		out.Changes = append(out.Changes, api.UnreadChange{
			ObjectId:  ch.ObjectId,
			Dataset:   ch.Dataset,
			RecordIds: ch.RecordIds,
			Tags:      ch.Tags,
			ChangeId:  ch.ChangeId,
		})
	}
	return c.JSON(http.StatusOK, out)
}

// parseReadLimit resolves the page size: the default when absent, the
// cap above it. ok is false for anything but a positive integer.
func parseReadLimit(raw string) (limit int, ok bool) {
	if raw == "" {
		return readStateDefaultLimit, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	return min(n, readStateMaxLimit), true
}

var errReadCursor = errors.New("malformed read cursor")

// formatReadCursor encodes a cursor as "<generation>.<stateSeq>".
func formatReadCursor(cur space.ReadCursor) string {
	return cur.Generation + "." + strconv.FormatUint(cur.StateSeq, 10)
}

// parseReadCursor decodes formatReadCursor's output; "" is the zero
// cursor. The seq is an apply seq, which fits 63 bits.
func parseReadCursor(raw string) (space.ReadCursor, error) {
	if raw == "" {
		return space.ReadCursor{}, nil
	}
	dot := strings.LastIndexByte(raw, '.')
	if dot <= 0 {
		return space.ReadCursor{}, errReadCursor
	}
	seq, err := strconv.ParseUint(raw[dot+1:], 10, 63)
	if err != nil {
		return space.ReadCursor{}, errReadCursor
	}
	return space.ReadCursor{Generation: raw[:dot], StateSeq: seq}, nil
}
