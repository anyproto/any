package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/anyproto/any-store/v2/query"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/anyuri"
	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

// Property & field descriptors — the server-side half of
// docs/27-descriptors.md. The SDK stores a definition's `x-format`
// object opaquely and enforces only `kind`; THIS server is the
// semantics boundary. Three layers live here:
//
//   - validateDescriptor — definition-write time (property add, dataset
//     field add): the keys the server interprets are typed, the slug is
//     checked against the pinned kind, reserved keys are refused,
//     vendor keys pass verbatim.
//   - patchPathToStorage / patchSetValue — the PATCH rules for both
//     surfaces: which paths are mutable, and the leaf-only rule (a set
//     never carries an object, so a container can only be unset).
//   - validateDescriptorValues — property-write time (propertiesSet,
//     objectCreate initialProperties, bundle rootProperties): every
//     value is checked against its property's CURRENT slug and rejected
//     when it does not fit. A write-boundary check, never a promise
//     about what is stored.
//
// Known gap: raw POST /modify on the `properties` dataset bypasses
// value validation (op paths would need parsing) — docs/03-api.md.

// Storage field names whose wire spelling differs. The SDK's typetype
// package is internal (not importable), so the wire→storage mapping
// lives here; these names are pinned by the SDK's property handler.
const (
	propFieldXKey    = "x-key"
	propFieldXFormat = "x-format"
	wireXFormat      = "xFormat"
)

// The descriptor keys the server interprets. Anything else at the top
// level is a vendor namespace, stored verbatim.
const (
	xfType     = "type"
	xfIcon     = "icon"
	xfPos      = "pos"
	xfOptions  = "options"
	xfRelation = "relation"
	xfConfig   = "config"
	// xfLinks marks a field whose value carries any:// references for
	// the link index (docs/13-index.md § Links): "link" (one string),
	// "links" (an array), "markdown" (text scanned for references) or
	// "none" (never scanned). The relation and markdown slugs imply it.
	xfLinks = "links"
	// Reserved for future contracts; refused today.
	xfValidate = "validate"
	xfCompute  = "compute"

	xfRelationTargets = "targetTypes"
	xfRelationFilter  = "filter"
	xfConfigMultiple  = "multiple"
	xfConfigMax       = "max"
)

// The v1 slug vocabulary. `type` is an open set — an unknown slug is
// never an error and gets no value checks — these are the slugs the
// server knows the shape of.
const (
	slugText     = "text"
	slugLongtext = "longtext"
	slugMarkdown = "markdown"
	slugURL      = "url"
	slugEmail    = "email"
	slugPhone    = "phone"
	slugChoice   = "choice"
	slugRelation = "relation"
	slugNumber   = "number"
	slugCurrency = "currency"
	slugPercent  = "percent"
	slugRating   = "rating"
	slugDuration = "duration"
	slugCheckbox = "checkbox"
	slugDate     = "date"
	slugDatetime = "datetime"
	slugPeriod   = "period"
	slugMoney    = "money"
	slugGeo      = "geo"
	// Reserved for the space-level shared tag table.
	slugTags = "tags"
)

// slugKinds is the kind each known slug requires. `kind` is pinned, so
// a slug can only ever move within one kind.
var slugKinds = map[string]string{
	slugText: api.PropertyKindString, slugLongtext: api.PropertyKindString, slugMarkdown: api.PropertyKindString,
	slugURL: api.PropertyKindString, slugEmail: api.PropertyKindString, slugPhone: api.PropertyKindString,
	slugChoice: api.PropertyKindArray, slugRelation: api.PropertyKindArray,
	slugNumber: api.PropertyKindNumber, slugCurrency: api.PropertyKindNumber, slugPercent: api.PropertyKindNumber,
	slugRating: api.PropertyKindNumber, slugDuration: api.PropertyKindNumber,
	slugCheckbox: api.PropertyKindBoolean,
	slugDate:     api.PropertyKindDatetime, slugDatetime: api.PropertyKindDatetime,
	slugPeriod: api.PropertyKindObject, slugMoney: api.PropertyKindObject, slugGeo: api.PropertyKindObject,
}

// slugKindMismatch reports why slug cannot sit on a property of kind
// ("" = fine). kind "" (undeclared, e.g. a stamped dataset field) skips
// the check.
func slugKindMismatch(slug, kind string) string {
	if slug == slugTags {
		return "slug \"tags\" is reserved until the space-level tag table lands"
	}
	want, known := slugKinds[slug]
	if !known || kind == "" || want == kind {
		return ""
	}
	return fmt.Sprintf("slug %q requires kind %s; the property's kind is %s (kind is pinned — define a new property)", slug, want, kind)
}

// linkModeKinds is the kind each link marker requires: one reference
// is a string, a list an array, scanned text a string.
var linkModeKinds = map[string]string{
	index.LinkModeOne: api.PropertyKindString, index.LinkModeMany: api.PropertyKindArray,
	index.LinkModeMarkdown: api.PropertyKindString,
}

// linkModeMismatch reports why a link marker cannot sit on a property
// of kind ("" = fine; kind "" skips the check).
func linkModeMismatch(mode, kind string) string {
	want, known := linkModeKinds[mode]
	if !known || kind == "" || want == kind {
		return ""
	}
	return fmt.Sprintf("links marker %q requires kind %s; the property's kind is %s", mode, want, kind)
}

// xfSlug reads the semantic slug off a stored descriptor ("" when
// absent or not a string).
func xfSlug(xf map[string]any) string {
	s, _ := xf[xfType].(string)
	return s
}

// xfConfigBool reads a boolean config flag off a stored descriptor;
// absent or non-boolean reads false.
func xfConfigBool(xf map[string]any, key string) bool {
	cfg, _ := xf[xfConfig].(map[string]any)
	b, _ := cfg[key].(bool)
	return b
}

// xfConfigNumber reads a numeric config setting; ok=false when absent
// or not a number.
func xfConfigNumber(xf map[string]any, key string) (float64, bool) {
	cfg, _ := xf[xfConfig].(map[string]any)
	f, ok := cfg[key].(float64)
	return f, ok
}

// ---------------------------------------------------------------------
// Definition-time validation
// ---------------------------------------------------------------------

// validateDescriptor checks a create-time xFormat body and decodes it
// for the SDK draft. kind is the definition's wire kind ("" = not
// declared, skips the slug check). Returns (nil, "", "") for an absent
// or null body; on failure code is request.invalid_field (not an
// object, an unaddressable key, wrong leaf type, unknown member of an
// interpreted container) or property.format_invalid (slug/kind
// mismatch, reserved slug or key, unparseable filter).
func validateDescriptor(raw json.RawMessage, kind string) (xf map[string]any, code, reason string) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, "", ""
	}
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	v, err := parser.ParseBytes(raw)
	if err != nil || v.Type() != fastjson.TypeObject {
		return nil, "request.invalid_field", "xFormat must be a JSON object"
	}
	if code, reason = checkDescriptorKeys(v, wireXFormat); code != "" {
		return nil, code, reason
	}
	obj, _ := v.Object()
	obj.Visit(func(k []byte, val *fastjson.Value) {
		if code != "" {
			return
		}
		key := string(k)
		switch key {
		case xfType, xfIcon, xfPos, xfLinks:
			code, reason = checkDescriptorLeaf([]string{key}, val)
		case xfOptions:
			code, reason = checkOptionsObject(val)
		case xfRelation:
			code, reason = checkMemberObject(key, val, map[string]bool{xfRelationTargets: true, xfRelationFilter: true})
		case xfConfig:
			code, reason = checkMemberObject(key, val, nil)
		case xfValidate, xfCompute:
			code, reason = "property.format_invalid", fmt.Sprintf("xFormat.%s is reserved for a future contract", key)
		default:
			// Vendor namespace: stored verbatim, never inspected.
		}
	})
	if code != "" {
		return nil, code, reason
	}
	if slug := string(v.GetStringBytes(xfType)); slug != "" {
		if r := slugKindMismatch(slug, kind); r != "" {
			return nil, "property.format_invalid", r
		}
	}
	if mode := string(v.GetStringBytes(xfLinks)); mode != "" {
		if r := linkModeMismatch(mode, kind); r != "" {
			return nil, "property.format_invalid", r
		}
	}
	if err := json.Unmarshal(raw, &xf); err != nil {
		return nil, "request.invalid_field", "xFormat must be a JSON object"
	}
	return xf, "", ""
}

// checkDescriptorKeys walks every object in a descriptor value —
// vendor subtrees and array elements included — and refuses keys the
// surface could not address or store faithfully: empty, containing "."
// (PATCH paths split on it, so the member could never be patched or
// unset on its own), or starting with "$" (the extended-JSON wrapper
// namespace — a `{"$date": …}` object converts to an instant on the way
// in and reads back as something else).
func checkDescriptorKeys(v *fastjson.Value, path string) (code, reason string) {
	switch v.Type() {
	case fastjson.TypeObject:
		obj, _ := v.Object()
		obj.Visit(func(k []byte, val *fastjson.Value) {
			if code != "" {
				return
			}
			key := string(k)
			if key == "" || strings.Contains(key, ".") || strings.HasPrefix(key, "$") {
				code, reason = "request.invalid_field",
					fmt.Sprintf("%s: key %q — descriptor keys are non-empty, contain no '.', and do not start with '$'", path, key)
				return
			}
			code, reason = checkDescriptorKeys(val, path+"."+key)
		})
	case fastjson.TypeArray:
		for i, el := range v.GetArray() {
			if code, reason = checkDescriptorKeys(el, fmt.Sprintf("%s[%d]", path, i)); code != "" {
				return code, reason
			}
		}
	}
	return code, reason
}

// checkOptionsObject validates a whole `options` map at create: every
// entry is an object of the known option leaves.
func checkOptionsObject(v *fastjson.Value) (code, reason string) {
	if v.Type() != fastjson.TypeObject {
		return "request.invalid_field", "xFormat.options must be an object keyed by option key"
	}
	opts, _ := v.Object()
	opts.Visit(func(k []byte, entry *fastjson.Value) {
		if code != "" {
			return
		}
		key := string(k)
		if key == "" {
			code, reason = "request.invalid_field", "xFormat.options has an empty option key"
			return
		}
		if entry.Type() != fastjson.TypeObject {
			code, reason = "request.invalid_field", fmt.Sprintf("xFormat.options.%s must be an object {name, color, pos, meta}", key)
			return
		}
		eo, _ := entry.Object()
		eo.Visit(func(lk []byte, leaf *fastjson.Value) {
			if code != "" {
				return
			}
			leafKey := string(lk)
			if leafKey == "meta" {
				if leaf.Type() != fastjson.TypeObject {
					code, reason = "request.invalid_field", fmt.Sprintf("xFormat.options.%s.meta must be an object of strings", key)
					return
				}
				mo, _ := leaf.Object()
				mo.Visit(func(mk []byte, mv *fastjson.Value) {
					if code == "" {
						code, reason = checkDescriptorLeaf([]string{xfOptions, key, "meta", string(mk)}, mv)
					}
				})
				return
			}
			code, reason = checkDescriptorLeaf([]string{xfOptions, key, leafKey}, leaf)
		})
	})
	return code, reason
}

// checkMemberObject validates one interpreted container (`relation`,
// `config`) at create: an object whose members are leaves; allowed
// restricts the member names (nil = any name, scalar leaves).
func checkMemberObject(member string, v *fastjson.Value, allowed map[string]bool) (code, reason string) {
	if v.Type() != fastjson.TypeObject {
		return "request.invalid_field", fmt.Sprintf("xFormat.%s must be an object", member)
	}
	obj, _ := v.Object()
	obj.Visit(func(k []byte, leaf *fastjson.Value) {
		if code != "" {
			return
		}
		key := string(k)
		if allowed != nil && !allowed[key] {
			code, reason = "request.invalid_field", fmt.Sprintf("xFormat.%s.%s is not a known member", member, key)
			return
		}
		code, reason = checkDescriptorLeaf([]string{member, key}, leaf)
	})
	return code, reason
}

// checkDescriptorLeaf types one leaf under x-format. segs is the path
// relative to the bag (e.g. ["options","high","color"]); v is the leaf
// value — never an object (the walkers descend containers, PATCH
// refuses them). Returns ("", "") when the leaf fits; the vendor
// namespace always fits.
func checkDescriptorLeaf(segs []string, v *fastjson.Value) (code, reason string) {
	path := wireXFormat + "." + strings.Join(segs, ".")
	wantString := func() (string, string) {
		if v.Type() != fastjson.TypeString {
			return "request.invalid_field", path + " must be a string"
		}
		return "", ""
	}
	switch segs[0] {
	case xfType:
		if c, r := wantString(); c != "" {
			return c, r
		}
		if len(v.GetStringBytes()) == 0 {
			return "property.format_invalid", path + " must be a non-empty slug"
		}
		if string(v.GetStringBytes()) == slugTags {
			return "property.format_invalid", slugKindMismatch(slugTags, "")
		}
		return "", ""
	case xfIcon, xfPos:
		return wantString()
	case xfLinks:
		if len(segs) != 1 {
			return "request.invalid_field", path + " is a leaf"
		}
		if c, r := wantString(); c != "" {
			return c, r
		}
		if !index.ValidLinkMode(string(v.GetStringBytes())) {
			return "property.format_invalid", path + " must be one of link, links, markdown, none"
		}
		return "", ""
	case xfOptions:
		// options.<key>.{name,color,pos} and options.<key>.meta.<k>
		if (len(segs) == 4 && segs[2] == "meta") || (len(segs) == 3 && (segs[2] == "name" || segs[2] == "color" || segs[2] == "pos")) {
			return wantString()
		}
		return "request.invalid_field", path + " is not an option leaf (name, color, pos, meta.<k>)"
	case xfRelation:
		if len(segs) != 2 {
			return "request.invalid_field", path + " is not a relation member"
		}
		switch segs[1] {
		case xfRelationTargets:
			if v.Type() != fastjson.TypeArray {
				return "request.invalid_field", path + " must be an array of type xKeys"
			}
			for _, el := range v.GetArray() {
				if s, ok := fastjsonString(el); !ok || s == "" {
					return "request.invalid_field", path + " must contain only non-empty strings"
				}
			}
			return "", ""
		case xfRelationFilter:
			if c, r := wantString(); c != "" {
				return c, r
			}
			if _, err := query.ParseCondition(string(v.GetStringBytes())); err != nil {
				return "property.format_invalid", fmt.Sprintf("%s does not parse as a query condition: %v", path, err)
			}
			return "", ""
		}
		return "request.invalid_field", path + " is not a known relation member (targetTypes, filter)"
	case xfConfig:
		if len(segs) != 2 {
			return "request.invalid_field", path + ": config holds scalar leaves only"
		}
		switch v.Type() {
		case fastjson.TypeString, fastjson.TypeNumber, fastjson.TypeTrue, fastjson.TypeFalse:
			return "", ""
		}
		return "request.invalid_field", path + " must be a scalar (string, number or boolean)"
	case xfValidate, xfCompute:
		return "property.format_invalid", fmt.Sprintf("xFormat.%s is reserved for a future contract", segs[0])
	}
	return "", "" // vendor namespace
}

// ---------------------------------------------------------------------
// PATCH paths and values (properties and dataset fields)
// ---------------------------------------------------------------------

// patchPathToStorage validates one property PATCH path against the
// mutable allowlist and translates it to the stored path. Returns
// (storagePath, "", "") on success, else ("", code, reason): pinned
// paths → property.immutable, unknown/malformed → request.invalid_field.
//
// forSet distinguishes $set from $unset. A $set targets a leaf, never
// a container — setting `meta`, `xFormat`, `xFormat.options`,
// `xFormat.options.<key>`, `xFormat.relation` or `xFormat.config` would
// replace the whole object and silently drop what other clients wrote,
// so those are rejected for $set. A $unset may name any of them to
// clear it.
func patchPathToStorage(path string, forSet bool) (storagePath, code, reason string) {
	const invalid = "request.invalid_field"
	segs, code, reason := splitPatchPath(path)
	if code != "" {
		return "", code, reason
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
	case "meta":
		switch {
		case len(segs) == 1:
			if forSet {
				return "", invalid, fmt.Sprintf("path %q: set meta.%s, not the whole map", path, index.MetaIndexKey)
			}
			return path, "", ""
		case len(segs) == 2 && segs[1] == index.MetaIndexKey:
			return path, "", ""
		}
		return "", invalid, fmt.Sprintf("path %q: meta holds only %s; descriptive keys live under %s", path, index.MetaIndexKey, wireXFormat)
	case wireXFormat:
		return xformatPatchPath(segs, path, forSet)
	case "kind", "scope", "items", "properties", "key", "id":
		return "", "property.immutable", fmt.Sprintf("path %q is immutable (define a new property to change it)", path)
	default:
		return "", invalid, fmt.Sprintf("unknown property path %q", path)
	}
}

// fieldPatchPathToStorage is patchPathToStorage for a dataset field
// record: name, description and every path under xFormat mutate; the
// behavioral declaration is pinned → dataset.immutable.
func fieldPatchPathToStorage(path string, forSet bool) (storagePath, code, reason string) {
	segs, code, reason := splitPatchPath(path)
	if code != "" {
		return "", code, reason
	}
	switch segs[0] {
	case "name", "description":
		if len(segs) != 1 {
			return "", "request.invalid_field", fmt.Sprintf("path %q takes no subpath", path)
		}
		return path, "", ""
	case wireXFormat:
		return xformatPatchPath(segs, path, forSet)
	}
	return "", "dataset.immutable", fmt.Sprintf("path %q is pinned; mutable paths: name, description, %s.*", path, wireXFormat)
}

func splitPatchPath(path string) (segs []string, code, reason string) {
	const invalid = "request.invalid_field"
	if path == "" {
		return nil, invalid, "empty patch path"
	}
	segs = strings.Split(path, ".")
	for _, s := range segs {
		if s == "" {
			return nil, invalid, fmt.Sprintf("path %q has an empty segment", path)
		}
		if strings.HasPrefix(s, "$") {
			return nil, invalid, fmt.Sprintf("path %q has a segment starting with '$' (reserved)", path)
		}
	}
	return segs, "", ""
}

// xformatPatchPath validates an `xFormat.*` PATCH path structurally —
// containers are unset-only, interpreted members take their known
// leaves, the vendor namespace is free-form — and translates the head
// to the stored `x-format`. Value typing is patchSetValue's job.
func xformatPatchPath(segs []string, path string, forSet bool) (string, string, string) {
	const invalid = "request.invalid_field"
	storage := propFieldXFormat + strings.TrimPrefix(path, wireXFormat)
	container := func() (string, string, string) {
		if forSet {
			return "", invalid, fmt.Sprintf("path %q: set a leaf, not a container (unset it to clear)", path)
		}
		return storage, "", ""
	}
	if len(segs) == 1 {
		return container()
	}
	switch segs[1] {
	case xfType, xfIcon, xfPos:
		if len(segs) != 2 {
			return "", invalid, fmt.Sprintf("path %q: %s.%s takes no subpath", path, wireXFormat, segs[1])
		}
		return storage, "", ""
	case xfOptions:
		// xFormat.options[.<key>[.<leaf>[.<k>]]]
		if len(segs) <= 3 {
			return container()
		}
		switch segs[3] {
		case "name", "color", "pos":
			if len(segs) != 4 {
				return "", invalid, fmt.Sprintf("path %q: option %s takes no subpath", path, segs[3])
			}
			return storage, "", ""
		case "meta":
			switch len(segs) {
			case 4:
				return container()
			case 5:
				return storage, "", ""
			}
			return "", invalid, fmt.Sprintf("path %q: option meta is a flat string map", path)
		}
		return "", invalid, fmt.Sprintf("path %q: unknown option leaf %q (name, color, pos, meta.<k>)", path, segs[3])
	case xfRelation:
		if len(segs) == 2 {
			return container()
		}
		if len(segs) != 3 || (segs[2] != xfRelationTargets && segs[2] != xfRelationFilter) {
			return "", invalid, fmt.Sprintf("path %q: relation members are %s and %s", path, xfRelationTargets, xfRelationFilter)
		}
		return storage, "", ""
	case xfConfig:
		switch len(segs) {
		case 2:
			return container()
		case 3:
			return storage, "", ""
		}
		return "", invalid, fmt.Sprintf("path %q: config holds scalar leaves only", path)
	case xfValidate, xfCompute:
		// Reserved: never written through this surface; an unset stays
		// available as the repair path for a key that arrived another way.
		if forSet {
			return "", "property.format_invalid", fmt.Sprintf("%s.%s is reserved for a future contract", wireXFormat, segs[1])
		}
		return storage, "", ""
	}
	// Vendor namespace: any depth. A bare `xFormat.<vendor>` set is a
	// leaf if its value is scalar — patchSetValue refuses objects.
	return storage, "", ""
}

// patchSetValue decodes and validates one Set value for a stored path.
// Outside x-format every leaf is a JSON string (null included in the
// refusal — a clear is an unset). Under x-format the leaf-only rule is
// structural — an object is refused whatever the path, so no set can
// replace a container — the interpreted leaves are typed by
// checkDescriptorLeaf, and vendor leaves take any non-object value
// whose nested keys are addressable. Returns (value, "", "") or
// (nil, code, reason).
func patchSetValue(storagePath string, raw json.RawMessage) (val any, code, reason string) {
	segs := strings.Split(storagePath, ".")
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	v, err := parser.ParseBytes(raw)
	if err != nil {
		return nil, "request.invalid_field", fmt.Sprintf("path %q value is not valid JSON", storagePath)
	}
	if segs[0] != propFieldXFormat {
		if v.Type() != fastjson.TypeString {
			return nil, "request.invalid_field", fmt.Sprintf("path %q value must be a JSON string", storagePath)
		}
		return string(v.GetStringBytes()), "", ""
	}
	if v.Type() == fastjson.TypeObject {
		return nil, "request.invalid_field", fmt.Sprintf("path %q: a set carries a leaf value, never an object — set its leaves, or unset the container", storagePath)
	}
	if c, r := checkDescriptorKeys(v, storagePath); c != "" {
		return nil, c, r
	}
	if len(segs) > 1 {
		if c, r := checkDescriptorLeaf(segs[1:], v); c != "" {
			return nil, c, r
		}
	}
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, "request.invalid_field", fmt.Sprintf("path %q value is not valid JSON", storagePath)
	}
	return val, "", ""
}

// ---------------------------------------------------------------------
// Value validation against the current slug
// ---------------------------------------------------------------------

// descriptorViolation describes one patch value that doesn't fit its
// property's current slug.
type descriptorViolation struct {
	PropId string
	Slug   string
	Reason string
}

// details renders the violation as the error-envelope details map.
func (v *descriptorViolation) details() map[string]any {
	return map[string]any{"propId": v.PropId, "format": v.Slug, "reason": v.Reason}
}

// validateDescriptorValues checks a property patch against the type's
// descriptor-bearing definitions. Values are the *fastjson.Value
// entries the property handlers build; nulls (unsets) always pass;
// properties with no slug — or propIds not defined at all (the SDK
// rejects those itself) — are skipped. Returns nil when everything
// fits.
func validateDescriptorValues(defs []space.PropertyDef, patch map[string]any) *descriptorViolation {
	var slugged map[string]*space.PropertyDef
	for i := range defs {
		if xfSlug(defs[i].XFormat) != "" {
			if slugged == nil {
				slugged = map[string]*space.PropertyDef{}
			}
			slugged[defs[i].Id] = &defs[i]
		}
	}
	if slugged == nil {
		return nil
	}
	for propId, raw := range patch {
		def, ok := slugged[propId]
		if !ok {
			continue
		}
		v, ok := raw.(*fastjson.Value)
		if !ok || v == nil || v.Type() == fastjson.TypeNull {
			continue
		}
		slug := xfSlug(def.XFormat)
		if reason := checkDescriptorValue(slug, def.XFormat, v); reason != "" {
			return &descriptorViolation{PropId: propId, Slug: slug, Reason: reason}
		}
	}
	return nil
}

// checkDescriptorValue validates one value's shape against a slug and
// its descriptor. Returns a human-readable reason, "" when it fits. An
// unknown slug fits anything — the SDK still enforces the kind.
func checkDescriptorValue(slug string, xf map[string]any, v *fastjson.Value) string {
	switch slug {
	case slugText, slugLongtext, slugPhone:
		if _, ok := fastjsonString(v); !ok {
			return slug + " value must be a string"
		}
	case slugMarkdown:
		if _, ok := fastjsonString(v); !ok {
			return "markdown value must be a string"
		}
	case slugURL:
		s, ok := fastjsonString(v)
		if !ok {
			return "url value must be a string"
		}
		if u, err := url.Parse(s); err != nil || u.Scheme == "" {
			return fmt.Sprintf("url value %q must be an absolute URL with a scheme", s)
		}
	case slugEmail:
		s, ok := fastjsonString(v)
		if !ok {
			return "email value must be a string"
		}
		at := strings.IndexByte(s, '@')
		if at <= 0 || at == len(s)-1 || strings.Count(s, "@") != 1 || strings.ContainsAny(s, " \t\n") {
			return fmt.Sprintf("email value %q must be one address, local@domain", s)
		}
	case slugChoice:
		arr, err := v.Array()
		if err != nil {
			return "choice value must be an array of option keys (a single choice is a one-element array)"
		}
		for _, el := range arr {
			if s, ok := fastjsonString(el); !ok || s == "" {
				return "choice value must contain only non-empty option keys"
			}
		}
		if len(arr) > 1 && !xfConfigBool(xf, xfConfigMultiple) {
			return "choice holds one value (config.multiple is off)"
		}
		// Membership in options is NOT enforced — dangling-tolerant: an
		// option may be deleted while values still reference its key.
	case slugRelation:
		arr, err := v.Array()
		if err != nil {
			return "relation value must be an array of any://<objectId> URIs (a single target is a one-element array)"
		}
		for _, el := range arr {
			s, ok := fastjsonString(el)
			if !ok {
				return "relation value must contain only strings"
			}
			// The bare in-space, fragment-less form; typed-kind URIs
			// are rejected here by design.
			if !anyuri.IsPropertyValueRef(s) {
				return fmt.Sprintf("link %q must be a plain \"any://<objectId>\" URI", s)
			}
		}
		if len(arr) > 1 && !xfConfigBool(xf, xfConfigMultiple) {
			return "relation holds one value (config.multiple is off)"
		}
	case slugNumber, slugCurrency, slugPercent, slugDuration:
		if v.Type() != fastjson.TypeNumber {
			return slug + " value must be a number"
		}
	case slugRating:
		if v.Type() != fastjson.TypeNumber {
			return "rating value must be a number"
		}
		f := v.GetFloat64()
		if max, ok := xfConfigNumber(xf, xfConfigMax); ok && (f < 0 || f > max) {
			return fmt.Sprintf("rating value %v must be between 0 and %v (config.max)", f, max)
		}
	case slugCheckbox:
		if v.Type() != fastjson.TypeTrue && v.Type() != fastjson.TypeFalse {
			return "checkbox value must be a boolean"
		}
	case slugDate:
		ts, reason := datetimeValue(v)
		if reason != "" {
			return reason
		}
		if !ts.Equal(ts.UTC().Truncate(24 * time.Hour)) {
			return fmt.Sprintf("date value %q must be midnight UTC", ts.UTC().Format(time.RFC3339))
		}
	case slugDatetime:
		_, reason := datetimeValue(v)
		return reason
	case slugPeriod:
		return checkPeriodValue(v)
	case slugMoney:
		return checkMoneyValue(v)
	case slugGeo:
		return checkGeoValue(v)
	}
	return ""
}

// checkPeriodValue: `{from?, to?}` — instants, at least one, to ≥ from.
// The compound is one value written whole: an open end is the key
// omitted, never a sub-path unset.
func checkPeriodValue(v *fastjson.Value) string {
	obj, err := v.Object()
	if err != nil {
		return `period value must be an object {"from": <instant>, "to": <instant>}`
	}
	var from, to time.Time
	var hasFrom, hasTo bool
	var bad string
	obj.Visit(func(k []byte, val *fastjson.Value) {
		if bad != "" {
			return
		}
		switch string(k) {
		case "from":
			ts, reason := datetimeValue(val)
			if reason != "" {
				bad = "period.from: " + reason
				return
			}
			from, hasFrom = ts, true
		case "to":
			ts, reason := datetimeValue(val)
			if reason != "" {
				bad = "period.to: " + reason
				return
			}
			to, hasTo = ts, true
		default:
			bad = fmt.Sprintf("period value has an unknown key %q (from, to)", k)
		}
	})
	if bad != "" {
		return bad
	}
	if !hasFrom && !hasTo {
		return "period value needs at least one of from, to"
	}
	if hasFrom && hasTo && to.Before(from) {
		return "period value must end on or after it starts"
	}
	return ""
}

// checkMoneyValue: exactly `{amount: number, currency: string}`.
func checkMoneyValue(v *fastjson.Value) string {
	obj, err := v.Object()
	if err != nil {
		return `money value must be an object {"amount": <number>, "currency": "<code>"}`
	}
	var hasAmount, hasCurrency bool
	var bad string
	obj.Visit(func(k []byte, val *fastjson.Value) {
		if bad != "" {
			return
		}
		switch string(k) {
		case "amount":
			if val.Type() != fastjson.TypeNumber {
				bad = "money.amount must be a number"
				return
			}
			hasAmount = true
		case "currency":
			if s, ok := fastjsonString(val); !ok || s == "" {
				bad = "money.currency must be a non-empty string"
				return
			}
			hasCurrency = true
		default:
			bad = fmt.Sprintf("money value has an unknown key %q (amount, currency)", k)
		}
	})
	if bad != "" {
		return bad
	}
	if !hasAmount || !hasCurrency {
		return "money value needs both amount and currency"
	}
	return ""
}

// checkGeoValue: exactly `{lat: number, lng: number}` in range.
func checkGeoValue(v *fastjson.Value) string {
	obj, err := v.Object()
	if err != nil {
		return `geo value must be an object {"lat": <number>, "lng": <number>}`
	}
	var hasLat, hasLng bool
	var bad string
	obj.Visit(func(k []byte, val *fastjson.Value) {
		if bad != "" {
			return
		}
		switch string(k) {
		case "lat":
			if val.Type() != fastjson.TypeNumber || val.GetFloat64() < -90 || val.GetFloat64() > 90 {
				bad = "geo.lat must be a number between -90 and 90"
				return
			}
			hasLat = true
		case "lng":
			if val.Type() != fastjson.TypeNumber || val.GetFloat64() < -180 || val.GetFloat64() > 180 {
				bad = "geo.lng must be a number between -180 and 180"
				return
			}
			hasLng = true
		default:
			bad = fmt.Sprintf("geo value has an unknown key %q (lat, lng)", k)
		}
	})
	if bad != "" {
		return bad
	}
	if !hasLat || !hasLng {
		return "geo value needs both lat and lng"
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

// xformatToWire renders a stored descriptor for a wire definition
// (nil → absent).
func xformatToWire(xf map[string]any) json.RawMessage {
	if len(xf) == 0 {
		return nil
	}
	b, err := json.Marshal(xf)
	if err != nil {
		return nil
	}
	return b
}
