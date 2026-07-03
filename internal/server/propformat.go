package server

import (
	"fmt"
	"time"

	"github.com/anyproto/any-store/v2/query"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/anyuri"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// Property formats: the SDK stores format annotations opaquely and
// enforces only their structure; THIS server is the semantics boundary.
// Two layers live here:
//
//   - validateFormatSemantics — definition-write time (typeAddProperty):
//     type/ui vocabulary, ui/type compatibility, filter parseability.
//   - validateFormatValues — property-write time (propertiesSet,
//     objectCreate initialProperties): value shapes per format (a
//     datetime parses, links are well-formed any:// URIs). No
//     object-existence or object-type checks.
//
// Known gap: raw POST /modify on the `properties` dataset bypasses
// value validation (op paths would need parsing) — see docs/03-api.md.

const dateLayout = "2006-01-02"

// formatUIs is the accepted presentation-hint vocabulary.
var formatUIs = map[string]struct{}{
	api.FormatUISelect:      {},
	api.FormatUIMultiselect: {},
	api.FormatUILink:        {},
	api.FormatUILinks:       {},
}

// validateFormatSemantics checks an incoming format declaration beyond
// the SDK's structural rules. kind is the request's wire kind ("" =
// defaulted from the format). Returns a human-readable reason ("" = ok).
func validateFormatSemantics(f *api.PropertyFormat, kind string) string {
	if f == nil {
		return ""
	}
	switch f.Type {
	case api.FormatTypeLinks:
		if kind != "" && kind != api.PropertyKindArray {
			return fmt.Sprintf("format %q requires kind array (or omit kind); got %q", f.Type, kind)
		}
	case api.FormatTypeDate, api.FormatTypeDatetime:
		if kind != "" && kind != api.PropertyKindString {
			return fmt.Sprintf("format %q requires kind string (or omit kind); got %q", f.Type, kind)
		}
		if f.UI != "" {
			return fmt.Sprintf("format %q takes no ui (got %q)", f.Type, f.UI)
		}
		if len(f.Filter) > 0 {
			return fmt.Sprintf("format %q takes no filter", f.Type)
		}
	case api.FormatTypeTags:
		return "format \"tags\" is reserved until the space-level tag table lands"
	default:
		return fmt.Sprintf("unknown format type %q (want links/date/datetime)", f.Type)
	}
	if f.UI != "" {
		if _, ok := formatUIs[f.UI]; !ok {
			return fmt.Sprintf("unknown format ui %q (want select/multiselect/link/links)", f.UI)
		}
	}
	if len(f.Filter) > 0 {
		if _, err := query.ParseCondition(string(f.Filter)); err != nil {
			return fmt.Sprintf("format filter does not parse as a query condition: %v", err)
		}
	}
	return ""
}

// formatDraftFromAPI maps the wire format onto the SDK draft. Call
// validateFormatSemantics first — this is a pure translation.
func formatDraftFromAPI(f *api.PropertyFormat) *space.PropertyFormatDraft {
	if f == nil {
		return nil
	}
	ft, _ := space.ParseFormatType(f.Type)
	draft := &space.PropertyFormatDraft{Type: ft, UI: f.UI}
	if len(f.Filter) > 0 {
		draft.Filter = string(f.Filter)
	}
	return draft
}

// formatToAPI maps a stored format back to the wire. A filter that
// somehow isn't valid JSON (written by another client through the
// opaque SDK path) is dropped rather than corrupting the response.
func formatToAPI(f *space.PropertyFormat) *api.PropertyFormat {
	if f == nil {
		return nil
	}
	out := &api.PropertyFormat{Type: f.Type.String(), UI: f.UI}
	if f.Filter != "" && fastjson.Validate(f.Filter) == nil {
		out.Filter = []byte(f.Filter)
	}
	return out
}

// formatViolation describes one patch value that doesn't match its
// property's declared format.
type formatViolation struct {
	PropId string
	Format string
	Reason string
}

// details renders the violation as the error-envelope details map.
func (v *formatViolation) details() map[string]any {
	return map[string]any{"propId": v.PropId, "format": v.Format, "reason": v.Reason}
}

// validateFormatValues checks a property patch against the type's
// format-bearing definitions. Values are the *fastjson.Value entries
// the property handlers build; nulls (unsets) always pass; properties
// without a format — or propIds not defined at all (the SDK rejects
// those itself) — are skipped. Returns nil when everything conforms.
func validateFormatValues(defs []space.PropertyDef, patch map[string]any) *formatViolation {
	var formats map[string]*space.PropertyDef
	for i := range defs {
		if defs[i].Format != nil {
			if formats == nil {
				formats = map[string]*space.PropertyDef{}
			}
			formats[defs[i].Id] = &defs[i]
		}
	}
	if formats == nil {
		return nil
	}
	for propId, raw := range patch {
		def, ok := formats[propId]
		if !ok {
			continue
		}
		v, ok := raw.(*fastjson.Value)
		if !ok || v == nil || v.Type() == fastjson.TypeNull {
			continue
		}
		if reason := checkFormatValue(def.Format.Type, v); reason != "" {
			return &formatViolation{PropId: propId, Format: def.Format.Type.String(), Reason: reason}
		}
	}
	return nil
}

// checkFormatValue validates one value's shape against a format type.
// Returns a human-readable reason, "" when conforming.
func checkFormatValue(ft space.FormatType, v *fastjson.Value) string {
	switch ft {
	case space.FormatDate:
		s, ok := fastjsonString(v)
		if !ok {
			return "date value must be a string"
		}
		if _, err := time.Parse(dateLayout, s); err != nil {
			return fmt.Sprintf("date value %q must be %q", s, dateLayout)
		}
	case space.FormatDatetime:
		s, ok := fastjsonString(v)
		if !ok {
			return "datetime value must be a string"
		}
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return fmt.Sprintf("datetime value %q must be RFC 3339", s)
		}
	case space.FormatLinks:
		arr, err := v.Array()
		if err != nil {
			return "links value must be an array of any:// URI strings"
		}
		for _, el := range arr {
			s, ok := fastjsonString(el)
			if !ok {
				return "links value must contain only strings"
			}
			// Property values use the in-space, fragment-less form.
			u, err := anyuri.Parse(s)
			if err != nil || u.SpaceId != "" || u.Fragment != "" {
				return fmt.Sprintf("link %q must be a plain \"any://<objectId>\" URI", s)
			}
		}
	case space.FormatTags:
		arr, err := v.Array()
		if err != nil {
			return "tags value must be an array of tag ids"
		}
		for _, el := range arr {
			if s, ok := fastjsonString(el); !ok || s == "" {
				return "tags value must contain only non-empty strings"
			}
		}
	}
	return ""
}

func fastjsonString(v *fastjson.Value) (string, bool) {
	if v == nil || v.Type() != fastjson.TypeString {
		return "", false
	}
	return string(v.GetStringBytes()), true
}
