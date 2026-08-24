package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/anyproto/any-store/v2/query"
	"github.com/anyproto/any-sync/app/logger"
	"github.com/anyproto/any-sync/commonspace/object/tree/treestorage"
	"github.com/anyproto/any-sync/commonspace/spacestorage"
	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"
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
// returns nil when ok is false. The message names what failed to parse
// and the expected body shape — a mute "invalid request body" leaves
// the caller (human or agent) guessing which part of the call was
// wrong.
func bindBody[T any](c echo.Context) (*T, bool) {
	req := new(T)
	if err := c.Bind(req); err != nil {
		_ = writeError(c, http.StatusBadRequest, "request.bad_json", bindErrorMessage[T](err), nil)
		return nil, false
	}
	return req, true
}

// bindBodyStrict is bindBody with unknown top-level keys rejected
// (400 request.unknown_field naming the accepted set). Use it on
// endpoints where a misplaced key silently loses the caller's intent —
// e.g. inline `properties` on type create, which no SDK surface
// accepts. hint, when non-empty, is appended to the unknown-field
// message to point at the right home for the value.
func bindBodyStrict[T any](c echo.Context, hint string) (*T, bool) {
	body, err := readBody(c)
	if err != nil {
		_ = writeError(c, http.StatusBadRequest, "request.bad_json", "read body: "+err.Error(), nil)
		return nil, false
	}
	req := new(T)
	if len(body) == 0 {
		return req, true
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(req); err != nil {
		_ = strictDecodeFailed(c, err, reflect.TypeFor[T](), hint, "", bindErrorMessage[T])
		return nil, false
	}
	return req, true
}

// strictDecodeFailed renders a DisallowUnknownFields decode failure
// with the shared envelope: the unknown field by name plus the
// accepted list (from acceptedType's json tags), or request.bad_json
// via msg. encoding/json exports no sentinel for the unknown-field
// rejection; the message form `json: unknown field "x"` is its
// documented shape. prefix, when non-empty, prefixes the bad-json
// message (e.g. the body field being decoded).
func strictDecodeFailed(c echo.Context, err error, acceptedType reflect.Type, hint, prefix string, msg func(error) string) error {
	if rest, ok := strings.CutPrefix(err.Error(), `json: unknown field `); ok {
		return unknownFieldRejected(c, []string{strings.Trim(rest, `"`)},
			jsonFieldNames(acceptedType), hint)
	}
	return writeError(c, http.StatusBadRequest, "request.bad_json", prefix+msg(err), nil)
}

// bindErrorMessage renders a JSON bind failure into a message that says
// what failed and what shape was expected. Never leaks Go type names —
// the expected shape is described by T's json field names.
func bindErrorMessage[T any](err error) string {
	fields := strings.Join(jsonFieldNames(reflect.TypeFor[T]()), ", ")
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) {
		if ute.Field != "" {
			return fmt.Sprintf("field %q expects JSON %s, got %s; expected body shape: {%s}",
				ute.Field, jsonTypeName(ute.Type), ute.Value, fields)
		}
		return fmt.Sprintf("request body must be a JSON object with fields {%s}, got JSON %s", fields, ute.Value)
	}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Sprintf("request body is not valid JSON (byte %d); expected a JSON object with fields {%s}",
			se.Offset, fields)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return fmt.Sprintf("request body is not valid JSON (unexpected end of input); expected a JSON object with fields {%s}", fields)
	}
	return fmt.Sprintf("invalid request body; expected a JSON object with fields {%s}", fields)
}

// jsonFieldNames lists a struct type's wire field names (json tags),
// the vocabulary bind/strict-bind errors echo back to the caller.
func jsonFieldNames(t reflect.Type) []string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var names []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			names = append(names, jsonFieldNames(f.Type)...)
			continue
		}
		if name == "" {
			name = f.Name
		}
		names = append(names, name)
	}
	return names
}

// jsonTypeName maps a Go type onto the JSON vocabulary for error
// messages ("string", "number", "array", "object", "boolean").
func jsonTypeName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	default:
		return "object"
	}
}

// checkUnknownFields rejects request-body keys outside the accepted
// set. Positive-extraction handlers (fastjson) never visit unknown
// keys, so without this gate a misplaced or misspelled field reads as
// success with the caller's intent silently dropped — a `filters` typo
// turns a filtered query into the whole space. A non-object body is
// rejected on the same grounds: no field of it would ever be read.
// hint, when non-empty, is appended to the unknown-field message to
// name the right home for the value. Returns (errResp, done=true) on
// rejection, mirroring the resolve* convention.
func checkUnknownFields(c echo.Context, root *fastjson.Value, hint string, accepted ...string) (error, bool) {
	if root == nil {
		return nil, false
	}
	obj, err := root.Object()
	if err != nil {
		return writeError(c, http.StatusBadRequest, "request.schema",
			"request body must be a JSON object; accepted fields: "+strings.Join(accepted, ", "),
			nil), true
	}
	var unknown []string
	obj.Visit(func(key []byte, _ *fastjson.Value) {
		if !slices.Contains(accepted, string(key)) {
			unknown = append(unknown, string(key))
		}
	})
	if len(unknown) == 0 {
		return nil, false
	}
	return unknownFieldRejected(c, unknown, accepted, hint), true
}

// unknownFieldRejected writes the 400 request.unknown_field envelope
// shared by checkUnknownFields (fastjson handlers) and bindBodyStrict
// (encoding/json handlers): every rejected key named, the accepted
// vocabulary enumerated, and the callsite hint pointing at where the
// value belongs.
func unknownFieldRejected(c echo.Context, unknown, accepted []string, hint string) error {
	noun := "field"
	if len(unknown) > 1 {
		noun = "fields"
	}
	msg := fmt.Sprintf(`unknown %s "%s"; accepted fields: %s`, noun,
		strings.Join(unknown, `", "`), strings.Join(accepted, ", "))
	if hint != "" {
		msg += "; " + hint
	}
	return writeError(c, http.StatusBadRequest, "request.unknown_field", msg,
		map[string]any{"fields": unknown, "accepted": accepted})
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
	if isSerializedNil(objectId) {
		return nil, "", serializedNilIdError(c, "objectId", objectId), true
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
	if isSerializedNil(objectId) {
		return nil, "", "", serializedNilIdError(c, "objectId", objectId), true
	}
	sp, errResp, done := d.resolveSpace(c)
	if done {
		return nil, "", "", errResp, true
	}
	return sp, objectId, msgId, nil, false
}

// isSerializedNil reports whether id is a stringified nil from a client
// runtime — Python's None, JSON/JS null and undefined, Go's <nil> —
// interpolated into a path or body by a caller whose id variable was
// unset. Such a value is never a real id, and letting it through reads
// as "object not found", sending the caller to investigate deletion
// instead of the unset variable.
func isSerializedNil(id string) bool {
	switch strings.ToLower(id) {
	case "none", "null", "nil", "undefined", "<nil>", "[object object]":
		return true
	}
	return false
}

// serializedNilIdError answers a serialized-nil id with a 400 that
// names the actual fault — the caller's variable, not the store.
func serializedNilIdError(c echo.Context, field, got string) error {
	return writeError(c, http.StatusBadRequest, "object.id_required",
		fmt.Sprintf("%s is %q — a serialized nil, not an id; the caller's %s variable was unset."+
			" Pass the id returned by create or query.", field, got, field),
		map[string]any{"field": field, "got": got})
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
	if resp, done := unsupportedError(c, err, details); done {
		return resp
	}
	if errors.Is(err, space.ErrObjectDeleted) {
		return writeError(c, http.StatusGone, codeObjectDeleted, "object is deleted", details)
	}
	// Read-state marks racing a space delete/removal (the space is
	// unknown, deleted, or pending at mark time) — a caller-visible
	// state, not a server fault.
	if errors.Is(err, space.ErrSpaceNotTracked) {
		return writeError(c, http.StatusNotFound, "space.not_found",
			"space is not tracked on this device (unknown, deleted, or join pending)", details)
	}
	// Deleted or unknown tree on a per-object op: the caller named an
	// object this space doesn't have (e.g. a stale search hit) — 404,
	// not a server fault. STOPGAP: any-sync sentinels until the SDK
	// exports space.ErrObjectNotFound (SYN-117).
	if errors.Is(err, spacestorage.ErrTreeStorageAlreadyDeleted) || errors.Is(err, treestorage.ErrUnknownTreeId) {
		return writeError(c, http.StatusNotFound, "object.not_found",
			"object not found in this space (unknown or deleted)", details)
	}
	if errors.Is(err, handler.ErrValidation) {
		return sdkValidationError(c, err, details)
	}
	// Filters are parsed at the request boundary (checkFilter), but a
	// ParseError can still ride an SDK op for filters assembled past it
	// — keep the mapping here as the fallback.
	var pe *query.ParseError
	if errors.As(err, &pe) {
		return filterParseError(c, pe, details)
	}
	handlerLog.Error("unclassified sdk error", zap.Error(err))
	return writeError(c, http.StatusInternalServerError, "internal", "internal error", details)
}

// filterOperators enumerates the filter grammar, derived from the
// grammar owner (query.Operators(), any-store#152) so the message
// cannot drift when any-store adds operators.
var filterOperators = strings.Join(query.Operators(), ", ")

// filterParseError answers a structured filter-parse rejection
// (query.ParseError, any-store#152) with a typed 400. A filter is
// caller-supplied, so a grammar violation is a client fault and must
// not surface as 500 internal — a 500 reads as "the server broke", so
// callers retry or report a server fault instead of fixing the filter.
//
// An operator-vocabulary miss keeps the documented
// filter.unknown_operator code, naming the offending token, the full
// grammar, and the array rule — the common way to arrive there is
// reaching for a $contains that does not exist: a scalar already
// compares against array elements, so {"tags": "x"} *is* contains
// (any-store Comp.Ok; docs/09-query.md § Arrays). Every other grammar
// violation is filter.invalid, located by the parser's path.
func filterParseError(c echo.Context, pe *query.ParseError, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	if pe.Path != "" {
		details["path"] = pe.Path
	}
	if pe.Op != "" {
		details["operator"] = pe.Op
	}
	if errors.Is(pe, query.ErrUnknownOperator) {
		msg := "unsupported filter operator " + pe.Op
		if pe.Path != "" {
			msg += " (at " + pe.Path + ")"
		}
		msg += "; supported: " + filterOperators +
			"; note a scalar compares against array elements, so {\"field\": \"value\"}" +
			" already matches any record whose array field contains that value"
		return writeError(c, http.StatusBadRequest, "filter.unknown_operator", msg, details)
	}
	msg := "invalid filter"
	if pe.Path != "" {
		msg += " at " + pe.Path
	}
	return writeError(c, http.StatusBadRequest, "filter.invalid", msg+": "+pe.Reason, details)
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
