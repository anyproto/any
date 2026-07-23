package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/anyproto/any-sync/app/logger"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/handler"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// handlerLog carries the raw errors behind sanitized 500 bodies —
// clients get code "internal" + a generic message, the log gets the
// full error.
var handlerLog = logger.NewNamed("handlers")

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

// bindBody decodes the JSON request body into T. On failure it writes
// the canonical 400 request.bad_json envelope itself; the caller just
// returns nil when ok is false.
func bindBody[T any](c echo.Context) (*T, bool) {
	req := new(T)
	if err := c.Bind(req); err != nil {
		_ = writeError(c, http.StatusBadRequest, "request.bad_json", "invalid request body", nil)
		return nil, false
	}
	return req, true
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

// resolveSpaceObject is resolveSpace plus the :objectId param check.
func (d *deps) resolveSpaceObject(c echo.Context) (space.Space, string, error, bool) {
	objectId := c.Param("objectId")
	if objectId == "" {
		return nil, "", writeError(c, http.StatusBadRequest, "request.missing_field", "objectId required", nil), true
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return nil, "", errResp, true
	}
	return sp, objectId, nil, false
}

// resolveSpaceObjectMsg is resolveSpaceObject plus the :msgId param
// check (the chat message routes).
func (d *deps) resolveSpaceObjectMsg(c echo.Context) (space.Space, string, string, error, bool) {
	objectId, msgId := c.Param("objectId"), c.Param("msgId")
	if objectId == "" || msgId == "" {
		return nil, "", "", writeError(c, http.StatusBadRequest, "request.missing_field", "objectId and msgId required", nil), true
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return nil, "", "", errResp, true
	}
	return sp, objectId, msgId, nil, false
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
	if errors.Is(err, space.ErrReadOnlySpace) {
		return writeError(c, http.StatusForbidden, "space.read_only",
			"space is read-only for this account (guest access or reader role)", details)
	}
	if errors.Is(err, handler.ErrValidation) {
		return sdkValidationError(c, err, details)
	}
	if op, ok := unknownFilterOperator(err); ok {
		return unknownFilterOperatorError(c, op, details)
	}
	handlerLog.Error("unclassified sdk error", zap.Error(err))
	return writeError(c, http.StatusInternalServerError, "internal", "internal error", details)
}

// filterOperators is the operator vocabulary any-store's filter parser
// accepts (the opBytes* constants in any-store/v2 query/cond_parse.go).
// Echoed back on a bad filter so the caller sees the whole grammar at once
// instead of discovering it one rejected token at a time.
const filterOperators = "$eq, $ne, $in, $nin, $all, $gt, $gte, $lt, $lte, " +
	"$exists, $type, $regex, $size, $text, $and, $or, $not, $nor"

// unknownOperatorPrefixes are any-store's message forms for an operator
// outside the grammar. Both spellings are listed because any-store#132
// fixes the "unknow" typo and lands after this — see unknownFilterOperator.
var unknownOperatorPrefixes = []string{"unknown operator: ", "unknow operator: "}

// unknownFilterOperator reports whether err is any-store's filter-parse
// rejection of an unrecognized operator, and recovers the offending token.
//
// STOPGAP: matched on message text. any-store does not export a sentinel
// for this yet; any-store#132 adds query.ErrUnknownOperator plus
// UnknownOperatorError{Op}, and the SDK already wraps the parse error with
// %w (spaceimpl.queryImpl.Filter), so once go.mod moves past that tag the
// whole function collapses into an errors.As and Op arrives structured. The
// bump needs no SDK release — `any` pins any-store/v2 directly, and module
// resolution takes the max of our pin and the SDK's. See SYN-79 / SYN-80.
func unknownFilterOperator(err error) (op string, ok bool) {
	msg := err.Error()
	for _, prefix := range unknownOperatorPrefixes {
		if i := strings.Index(msg, prefix); i >= 0 {
			return msg[i+len(prefix):], true
		}
	}
	return "", false
}

// unknownFilterOperatorError answers a bad filter operator with a typed 400.
// A filter is caller-supplied, so an operator outside the grammar is a client
// fault and must not surface as 500 internal — a 500 reads as "the server
// broke", so callers retry or report a server fault instead of fixing the
// filter.
//
// The message names the offending operator, the full grammar, and the array
// rule, because the common way to arrive here is reaching for a $contains
// that does not exist: a scalar already compares against array elements, so
// {"tags": "x"} *is* contains (any-store Comp.Ok; docs/09-query.md § Arrays).
func unknownFilterOperatorError(c echo.Context, op string, details map[string]any) error {
	msg := "unsupported filter operator " + op + "; supported: " + filterOperators +
		"; note a scalar compares against array elements, so {\"field\": \"value\"}" +
		" already matches any record whose array field contains that value"
	if details == nil {
		details = map[string]any{}
	}
	details["operator"] = op
	return writeError(c, http.StatusBadRequest, "filter.unknown_operator", msg, details)
}

// sdkValidationError maps the SDK's write-time property schema rejection
// (handler.ErrValidation) onto a 400. A rejected write is a caller error,
// not a server fault, so it must not surface as 500. The SDK's message is
// agent-readable and carries only caller-supplied type/property ids and
// JSON kind names — no internal Go types or filesystem paths — so it is
// safe to surface verbatim.
//
// The specific code comes from the SDK's structural classifier
// (handler.ClassifyValidation), yielding the documented codes in
// docs/06-errors.md. The status is always 400; an unclassifiable or
// unmapped reason falls back to the generic dataset.validation.
func sdkValidationError(c echo.Context, err error, details map[string]any) error {
	code := "dataset.validation"
	if reason, ok := handler.ClassifyValidation(err); ok {
		switch reason {
		case handler.ReasonKindMismatch:
			code = "property.kind_mismatch"
		case handler.ReasonUnknownProperty:
			code = "property.not_found"
		}
	}
	return writeError(c, http.StatusBadRequest, code, err.Error(), details)
}
