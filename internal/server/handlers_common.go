package server

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// readBody slurps the request body. Echo's BodyLimit middleware enforces
// the upper bound; we just need the bytes for fastjson's ParseBytes.
func readBody(c echo.Context) ([]byte, error) {
	r := c.Request().Body
	if r == nil {
		return nil, nil
	}
	defer r.Close()
	return io.ReadAll(r)
}

// resolveSpace fetches the Space for the :spaceId path param. On error
// returns a written response — the caller propagates it directly.
func (d *deps) resolveSpace(c echo.Context) (space.Space, error, bool) {
	id := c.Param("spaceId")
	if id == "" {
		return nil, writeError(c, http.StatusBadRequest, "request.missing_field", "spaceId required", nil), true
	}
	sp, err := d.sdk.Spaces().Get(c.Request().Context(), id)
	if err != nil {
		return nil, spaceError(c, err, id), true
	}
	return sp, nil, false
}

// modifyResultToAPI normalises space.ModifyResult onto the wire shape.
// Always returns a non-nil RecordIds slice so the JSON output keeps
// the [] zero-value rather than null. Rejections is omitted (omitempty)
// when nothing was dropped — present and non-empty signals partial
// success.
func modifyResultToAPI(r space.ModifyResult) api.ModifyResult {
	ids := r.RecordIds
	if ids == nil {
		ids = []string{}
	}
	out := api.ModifyResult{
		VersionId: string(r.VersionId),
		ChangeId:  r.ChangeId,
		RecordIds: ids,
	}
	if len(r.Rejections) > 0 {
		out.Rejections = make([]api.OpRejection, 0, len(r.Rejections))
		for _, rej := range r.Rejections {
			out.Rejections = append(out.Rejections, api.OpRejection{
				RecordIndex: rej.RecordIndex,
				RecordId:    rej.RecordId,
				OpIndex:     rej.OpIndex,
				Reason:      rej.Reason,
			})
		}
	}
	return out
}

// sdkOpError maps SDK-side errors from object/type/property surfaces to
// the canonical envelope. Conservative mirror of spaceError until
// the SDK exports comparable sentinels.
func sdkOpError(c echo.Context, err error, details map[string]any) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return writeError(c, http.StatusServiceUnavailable, "server.unavailable", "request cancelled", nil)
	}
	return writeError(c, http.StatusInternalServerError, "internal", err.Error(), details)
}
