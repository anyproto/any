package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/anyproto/any-store/v2/query"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/anyuri"
	"github.com/anyproto/any/internal/api"
)

// Storage field names for the two property-def fields whose wire name
// differs from their stored name. The SDK's typetype package is internal
// (not importable), so the wire→storage mapping for PatchProperty lives
// here; these names are pinned by the SDK's property handler version.
const (
	propFieldXKey  = "x-key"
	propFieldXKind = "x-kind"
)

// patchPathToStorage validates one wire PATCH path against the mutable
// allowlist and translates it to the stored path. Returns
// (storagePath, "", "") on success, else ("", code, reason): pinned
// paths → property.immutable, unknown/malformed → request.invalid_field.
//
// forSet distinguishes $set from $unset. A $set must always target a
// scalar leaf — setting a bare container (`meta`, `format.meta`,
// `format.options`, `format.options.<key>`) would store a string over
// the whole object and silently wipe it, so those are rejected for
// $set. A $unset may name a container to clear it (e.g. unset a whole
// option subtree).
func patchPathToStorage(path string, forSet bool) (storagePath, code, reason string) {
	const invalid = "request.invalid_field"
	if path == "" {
		return "", invalid, "empty patch path"
	}
	segs := strings.Split(path, ".")
	for _, s := range segs {
		if s == "" {
			return "", invalid, fmt.Sprintf("path %q has an empty segment", path)
		}
	}
	// bag validates a dotted string-bag path: `<head>` / `<head>.<key>`.
	// $set must target a key; $unset may name the whole bag (clear).
	bag := func(depthOfBag int) (string, string, string) {
		switch {
		case len(segs) == depthOfBag: // the bare container
			if forSet {
				return "", invalid, fmt.Sprintf("path %q: set a key, not the whole map", path)
			}
			return path, "", ""
		case len(segs) == depthOfBag+1: // <container>.<key>
			return path, "", ""
		default:
			return "", invalid, fmt.Sprintf("path %q: this bag is a flat string map", path)
		}
	}
	scalar := func(storage string) (string, string, string) {
		if len(segs) != 1 {
			return "", invalid, fmt.Sprintf("path %q takes no subpath", path)
		}
		return storage, "", ""
	}
	switch segs[0] {
	case "name", "description":
		return scalar(path)
	case "xKey":
		return scalar(propFieldXKey)
	case "xKind":
		return scalar(propFieldXKind)
	case "meta":
		return bag(1)
	case "format":
		return patchFormatPath(segs, path, forSet, bag)
	case "kind", "scope", "items", "properties", "key", "id":
		return "", "property.immutable", fmt.Sprintf("path %q is immutable (define a new property to change it)", path)
	default:
		return "", invalid, fmt.Sprintf("unknown property path %q", path)
	}
}

// patchFormatPath validates a `format.*` PATCH path. Pinned: the whole
// `format` object and `format.type`. Scalar leaves: format.ui,
// format.filter. Bags: format.meta.<k>. Options:
// format.options.<key>.{name,color,pos} and .meta.<k>; a $set must hit a
// leaf, a $unset may name a whole option (`format.options.<key>`) or the
// whole map (`format.options`).
func patchFormatPath(segs []string, path string, forSet bool, bag func(int) (string, string, string)) (string, string, string) {
	const invalid = "request.invalid_field"
	if len(segs) == 1 {
		return "", "property.immutable", fmt.Sprintf("path %q: the whole format object is immutable; patch a leaf", path)
	}
	switch segs[1] {
	case "type":
		return "", "property.immutable", fmt.Sprintf("path %q (format.type) is immutable", path)
	case "ui", "filter":
		if len(segs) != 2 {
			return "", invalid, fmt.Sprintf("path %q: format.%s takes no subpath", path, segs[1])
		}
		return path, "", ""
	case "meta":
		return bag(2)
	case "options":
		// format.options[.<key>[.<leaf>]]
		if len(segs) <= 3 { // whole map (2) or whole option (3)
			if forSet {
				return "", invalid, fmt.Sprintf("path %q: set an option leaf (…<key>.name|color|pos|meta.<k>), not a container", path)
			}
			return path, "", ""
		}
		switch segs[3] {
		case "name", "color", "pos":
			if len(segs) != 4 {
				return "", invalid, fmt.Sprintf("path %q: option %s takes no subpath", path, segs[3])
			}
			return path, "", ""
		case "meta":
			return bag(4)
		default:
			return "", invalid, fmt.Sprintf("path %q: unknown option leaf %q", path, segs[3])
		}
	default:
		return "", invalid, fmt.Sprintf("path %q: unknown format field %q", path, segs[1])
	}
}

// patchSetValue decodes/validates one Set value for a stored path. Every
// leaf is a JSON string except format.filter, which is a condition
// object stored as its JSON text. Returns (value, "", "") on success or
// (nil, code, reason). The code is format-specific (property.format_invalid)
// only for genuine format problems; a plain non-string value on any leaf
// is request.invalid_field.
func patchSetValue(storagePath string, raw json.RawMessage) (val any, code, reason string) {
	if storagePath == "format.filter" {
		if _, err := query.ParseCondition(string(raw)); err != nil {
			return nil, "property.format_invalid", fmt.Sprintf("format filter does not parse as a query condition: %v", err)
		}
		return string(raw), "", ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, "request.invalid_field", fmt.Sprintf("path %q value must be a JSON string", storagePath)
	}
	if storagePath == "format.ui" {
		if _, ok := formatUIs[s]; !ok {
			return nil, "property.format_invalid", fmt.Sprintf("unknown format ui %q (want select/multiselect/link/links)", s)
		}
	}
	return s, "", ""
}

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
	case api.FormatTypeMultiselect:
		if kind != "" && kind != api.PropertyKindArray {
			return fmt.Sprintf("format %q requires kind array (or omit kind); got %q", f.Type, kind)
		}
	case api.FormatTypeSelect:
		if kind != "" && kind != api.PropertyKindString {
			return fmt.Sprintf("format %q requires kind string (or omit kind); got %q", f.Type, kind)
		}
	case api.FormatTypeDate, api.FormatTypeDatetime:
		// datetime is what these formats imply; string is the legacy
		// ISO-8601 convention, kept because kind is pinned at first write
		// and properties created under the old default must stay usable.
		if kind != "" && kind != api.PropertyKindDatetime && kind != api.PropertyKindString {
			return fmt.Sprintf("format %q requires kind datetime or string (or omit kind); got %q", f.Type, kind)
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
		return fmt.Sprintf("unknown format type %q (want links/date/datetime/select/multiselect)", f.Type)
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
	draft := &space.PropertyFormatDraft{Type: ft, UI: f.UI, Meta: f.Meta}
	if len(f.Filter) > 0 {
		draft.Filter = string(f.Filter)
	}
	if len(f.Options) > 0 {
		draft.Options = optionsFromAPI(f.Options)
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
	out := &api.PropertyFormat{Type: f.Type.String(), UI: f.UI, Meta: f.Meta}
	if f.Filter != "" && fastjson.Validate(f.Filter) == nil {
		out.Filter = []byte(f.Filter)
	}
	if len(f.Options) > 0 {
		out.Options = optionsToAPI(f.Options)
	}
	return out
}

// optionsFromAPI / optionsToAPI translate the select/multiselect option
// map between the wire and SDK shapes (identical field-for-field).
func optionsFromAPI(in map[string]api.PropertyOption) map[string]space.PropertyOption {
	out := make(map[string]space.PropertyOption, len(in))
	for k, o := range in {
		out[k] = space.PropertyOption{Name: o.Name, Color: o.Color, Pos: o.Pos, Meta: o.Meta}
	}
	return out
}

func optionsToAPI(in map[string]space.PropertyOption) map[string]api.PropertyOption {
	out := make(map[string]api.PropertyOption, len(in))
	for k, o := range in {
		out[k] = api.PropertyOption{Name: o.Name, Color: o.Color, Pos: o.Pos, Meta: o.Meta}
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
		if reason := checkFormatValue(def, v); reason != "" {
			return &formatViolation{PropId: propId, Format: def.Format.Type.String(), Reason: reason}
		}
	}
	return nil
}

// checkFormatValue validates one value's shape against its property
// definition — the format, plus the kind for the two formats that carry
// both a datetime and a legacy string form. Returns a human-readable
// reason, "" when conforming.
func checkFormatValue(def *space.PropertyDef, v *fastjson.Value) string {
	switch def.Format.Type {
	case space.FormatDate:
		if def.Kind == space.PropertyKindDatetime {
			ts, reason := datetimeValue(v)
			if reason != "" {
				return reason
			}
			if !ts.Equal(ts.UTC().Truncate(24 * time.Hour)) {
				return fmt.Sprintf("date value %q must be midnight UTC", ts.UTC().Format(time.RFC3339))
			}
			return ""
		}
		s, ok := fastjsonString(v)
		if !ok {
			return "date value must be a string"
		}
		if _, err := time.Parse(dateLayout, s); err != nil {
			return fmt.Sprintf("date value %q must be %q", s, dateLayout)
		}
	case space.FormatDatetime:
		if def.Kind == space.PropertyKindDatetime {
			_, reason := datetimeValue(v)
			return reason
		}
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
			// Property values use the bare in-space, fragment-less
			// form; typed-kind URIs are rejected here by design.
			if !anyuri.IsPropertyValueRef(s) {
				return fmt.Sprintf("link %q must be a plain \"any://<objectId>\" URI", s)
			}
		}
	case space.FormatTags, space.FormatMultiselect:
		arr, err := v.Array()
		if err != nil {
			return "value must be an array of option keys"
		}
		for _, el := range arr {
			if s, ok := fastjsonString(el); !ok || s == "" {
				return "value must contain only non-empty option keys"
			}
		}
	case space.FormatSelect:
		// Shape only — membership in Format.Options is NOT enforced
		// (dangling-tolerant: an option may be deleted while values
		// still reference its key).
		if _, ok := fastjsonString(v); !ok {
			return "select value must be a string option key"
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

// datetimeValue reads the extended-JSON instant a datetime-kind property
// takes — `{"$date": "<RFC 3339>"}` or `{"$date": <unix millis>}`, the
// same shape reads return. Returns the instant, or the reason it is not
// one.
func datetimeValue(v *fastjson.Value) (time.Time, string) {
	obj, err := v.Object()
	if err != nil {
		return time.Time{}, `datetime value must be {"$date": "<RFC 3339>"} or {"$date": <unix millis>}`
	}
	dv := obj.Get("$date")
	if dv == nil || obj.Len() != 1 {
		return time.Time{}, `datetime value must be an object with exactly one key, "$date"`
	}
	switch dv.Type() {
	case fastjson.TypeString:
		s := string(dv.GetStringBytes())
		ts, perr := time.Parse(time.RFC3339Nano, s)
		if perr != nil {
			return time.Time{}, fmt.Sprintf("datetime value %q must be RFC 3339", s)
		}
		return ts, ""
	case fastjson.TypeNumber:
		// int64(float64), the same narrowing any-store's extended-JSON
		// decoder applies — so validation and the stored instant agree
		// on non-integer or very large literals.
		return time.UnixMilli(int64(dv.GetFloat64())).UTC(), ""
	}
	return time.Time{}, `"$date" must be an RFC 3339 string or unix millis`
}
