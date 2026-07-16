// Package anyuri builds and parses any:// URIs — the canonical string
// convention for referencing anything inside any: an object, a dataset
// record, an identity (mention), a space, a property value, a file.
//
// The typed grammar (docs/19-links.md is the semantics contract):
//
//	any://<kind>/<spaceId>[/<rest…>][?<params>][#<fragment>]
//
//	any://o/<spaceId>/<objectId>                        object
//	any://o/<spaceId>/<objectId>/<dataset>/<recordId>   dataset record
//	any://m/<spaceId>/<identity>                        identity (mention)
//	any://s/<spaceId>                                   space
//	any://p/<spaceId>/<objectId>/<propId>               property value
//	any://f/<spaceId>/<fileId>[?variant=<tag>]          file (files-v2)
//
// plus the two legacy bare forms, which keep parsing as kind "o"
// (Legacy: true) and are never rewritten:
//
//	any://<objectId>            in-space reference (the property-value form)
//	any://<spaceId>/<objectId>  global reference
//
// Kind slugs are a reserved lexical namespace: a first path segment of
// 1-4 lowercase-alphanumeric bytes is always a kind slug, anything
// longer is a legacy bare id. Real ids are ≥40-char base58 strings, so
// no collision is possible; the one behavior delta vs the pre-typed
// parser is that a pathological ≤4-char object id no longer parses.
// Future kinds must keep their slugs within this shape.
//
// The kind set is open. A well-formed URI with an unrecognized kind
// slug (including the reserved invite kind "i") parses to
// ErrKindUnknown — the caller's "degrade to a plain link" branch —
// which is distinct from ErrInvalid ("reject"). Unknown query params
// are ignored, never a failure. #fragment is a view-level anchor only
// and never identifies a data record.
//
// Property values of link-format properties use the strict bare
// one-segment, fragment-less form; check IsPropertyValueRef.
package anyuri

import (
	"errors"
	"fmt"
	"strings"
)

// Scheme is the URI scheme, without separators.
const Scheme = "any"

const prefix = Scheme + "://"

// Kind is the first path segment of a typed URI — what the link points
// at. The set is open; unknown kinds parse to ErrKindUnknown.
type Kind string

const (
	KindObject  Kind = "o" // object, or a record via /<dataset>/<recordId>
	KindMention Kind = "m" // identity / member (a mention)
	KindSpace   Kind = "s" // a space
	KindProp    Kind = "p" // a property value on an object
	KindFile    Kind = "f" // a file (files-v2 attach), variant via ?variant=

	// "i" (invite) is reserved in the registry but deliberately NOT
	// known here: it parses as ErrKindUnknown until implemented, which
	// is exactly the degrade-to-plain-link behavior the extension
	// policy prescribes.
)

// ErrInvalid is wrapped by every malformed-URI parse failure; classify
// with errors.Is. A consumer rejects these.
var ErrInvalid = errors.New("anyuri: invalid any:// URI")

// ErrKindUnknown is returned for a well-formed typed URI whose kind
// slug this parser doesn't know (a future kind, or the reserved "i").
// It does NOT wrap ErrInvalid: errors.Is(err, ErrKindUnknown) is the
// renderer's "degrade to a plain, non-magic link" branch — don't
// error, don't guess.
var ErrKindUnknown = errors.New("anyuri: unknown link kind")

// URI is a parsed any:// reference. Kind is always set on success;
// which other fields are populated depends on it. Legacy marks the two
// pre-typed bare forms (kind "o"; SpaceId empty for the in-space
// property-value form). Known params become fields (Variant); unknown
// params are dropped on Parse and do not round-trip.
type URI struct {
	Kind     Kind
	SpaceId  string // every typed kind; empty only for the legacy in-space form
	ObjectId string // o, p
	Dataset  string // o record path …/<dataset>/<recordId>
	RecordId string // o record path
	PropId   string // p
	Identity string // m
	FileId   string // f
	Variant  string // f: ?variant=<tag>
	Fragment string // view-level anchor; never a data-record id
	Legacy   bool   // parsed from a bare pre-typed form
}

// BuildObject returns the bare in-space reference "any://<objectId>" —
// the strict form links-format property values require. New typed
// references should prefer BuildObjectGlobal.
func BuildObject(objectId string) string {
	return prefix + objectId
}

// BuildObjectGlobal returns "any://o/<spaceId>/<objectId>".
func BuildObjectGlobal(spaceId, objectId string) string {
	return prefix + string(KindObject) + "/" + spaceId + "/" + objectId
}

// BuildRecord returns "any://o/<spaceId>/<objectId>/<dataset>/<recordId>"
// — a record inside an object's dataset (editor block, chat message).
func BuildRecord(spaceId, objectId, dataset, recordId string) string {
	return prefix + string(KindObject) + "/" + spaceId + "/" + objectId + "/" + dataset + "/" + recordId
}

// BuildMention returns "any://m/<spaceId>/<identity>".
func BuildMention(spaceId, identity string) string {
	return prefix + string(KindMention) + "/" + spaceId + "/" + identity
}

// BuildSpace returns "any://s/<spaceId>".
func BuildSpace(spaceId string) string {
	return prefix + string(KindSpace) + "/" + spaceId
}

// BuildProp returns "any://p/<spaceId>/<objectId>/<propId>" — a
// property VALUE on an object, keyed by the content-addressed propId
// (never xKey).
func BuildProp(spaceId, objectId, propId string) string {
	return prefix + string(KindProp) + "/" + spaceId + "/" + objectId + "/" + propId
}

// BuildFile returns "any://f/<spaceId>/<fileId>". fileId is the
// payloads-row id (per-attach, space-unique), never rootCid.
func BuildFile(spaceId, fileId string) string {
	return prefix + string(KindFile) + "/" + spaceId + "/" + fileId
}

// BuildFileVariant returns "any://f/<spaceId>/<fileId>?variant=<tag>".
func BuildFileVariant(spaceId, fileId, variant string) string {
	return BuildFile(spaceId, fileId) + "?variant=" + variant
}

// String renders the URI back to its text form. Legacy URIs re-render
// in their bare form (stored values are never rewritten).
func (u URI) String() string {
	var b strings.Builder
	b.WriteString(prefix)
	if u.Legacy || u.Kind == "" {
		if u.SpaceId != "" {
			b.WriteString(u.SpaceId)
			b.WriteByte('/')
		}
		b.WriteString(u.ObjectId)
	} else {
		b.WriteString(string(u.Kind))
		b.WriteByte('/')
		b.WriteString(u.SpaceId)
		switch u.Kind {
		case KindObject:
			b.WriteByte('/')
			b.WriteString(u.ObjectId)
			if u.Dataset != "" {
				b.WriteByte('/')
				b.WriteString(u.Dataset)
				b.WriteByte('/')
				b.WriteString(u.RecordId)
			}
		case KindMention:
			b.WriteByte('/')
			b.WriteString(u.Identity)
		case KindSpace:
		case KindProp:
			b.WriteByte('/')
			b.WriteString(u.ObjectId)
			b.WriteByte('/')
			b.WriteString(u.PropId)
		case KindFile:
			b.WriteByte('/')
			b.WriteString(u.FileId)
			if u.Variant != "" {
				b.WriteString("?variant=")
				b.WriteString(u.Variant)
			}
		}
	}
	if u.Fragment != "" {
		b.WriteByte('#')
		b.WriteString(u.Fragment)
	}
	return b.String()
}

// Parse decodes any of the typed forms plus the two legacy bare forms.
// Malformed input wraps ErrInvalid; a well-formed URI of an unknown
// kind wraps ErrKindUnknown (see the package doc for the distinction).
func Parse(s string) (URI, error) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return URI{}, fmt.Errorf("%w: missing %q prefix in %q", ErrInvalid, prefix, s)
	}
	var u URI
	rest, u.Fragment, _ = strings.Cut(rest, "#")
	rest, rawParams, _ := strings.Cut(rest, "?")
	if rest == "" {
		return URI{}, fmt.Errorf("%w: empty path in %q", ErrInvalid, s)
	}
	segs := strings.Split(rest, "/")
	for _, seg := range segs {
		if seg == "" {
			return URI{}, fmt.Errorf("%w: empty path segment in %q", ErrInvalid, s)
		}
	}
	if !isKindSlug(segs[0]) {
		// Legacy bare forms — first segment is an id.
		u.Kind, u.Legacy = KindObject, true
		switch len(segs) {
		case 1:
			u.ObjectId = segs[0]
		case 2:
			u.SpaceId, u.ObjectId = segs[0], segs[1]
		default:
			return URI{}, fmt.Errorf("%w: too many path segments in %q", ErrInvalid, s)
		}
		return u, nil
	}
	u.Kind = Kind(segs[0])
	args := segs[1:]
	arity := func(n int) error {
		if len(args) != n {
			return fmt.Errorf("%w: kind %q wants %d path segments after the kind, got %d in %q",
				ErrInvalid, u.Kind, n, len(args), s)
		}
		return nil
	}
	switch u.Kind {
	case KindObject:
		switch len(args) {
		case 2:
			u.SpaceId, u.ObjectId = args[0], args[1]
		case 4:
			u.SpaceId, u.ObjectId, u.Dataset, u.RecordId = args[0], args[1], args[2], args[3]
		default:
			// Includes the reserved 5-segment record-field form —
			// grammar room only, rejected until a consumer needs it.
			return URI{}, fmt.Errorf("%w: kind %q wants 2 or 4 path segments after the kind, got %d in %q",
				ErrInvalid, u.Kind, len(args), s)
		}
	case KindMention:
		if err := arity(2); err != nil {
			return URI{}, err
		}
		u.SpaceId, u.Identity = args[0], args[1]
	case KindSpace:
		if err := arity(1); err != nil {
			return URI{}, err
		}
		u.SpaceId = args[0]
	case KindProp:
		if err := arity(3); err != nil {
			return URI{}, err
		}
		u.SpaceId, u.ObjectId, u.PropId = args[0], args[1], args[2]
	case KindFile:
		if err := arity(2); err != nil {
			return URI{}, err
		}
		u.SpaceId, u.FileId = args[0], args[1]
		u.Variant = paramValue(rawParams, "variant")
	default:
		return URI{}, fmt.Errorf("%w: %q in %q", ErrKindUnknown, segs[0], s)
	}
	return u, nil
}

// IsValid reports whether s parses as a known any:// URI (unknown
// kinds are not valid — but distinguish them via Parse when degrading
// instead of rejecting).
func IsValid(s string) bool {
	_, err := Parse(s)
	return err == nil
}

// IsPropertyValueRef reports whether s is exactly the strict form
// links-format property values require: the bare one-segment,
// fragment-less "any://<objectId>".
func IsPropertyValueRef(s string) bool {
	u, err := Parse(s)
	return err == nil && u.Legacy && u.SpaceId == "" && u.Fragment == ""
}

// isKindSlug reports whether a first path segment sits in the reserved
// kind namespace: 1-4 lowercase-alphanumeric bytes.
func isKindSlug(s string) bool {
	if len(s) == 0 || len(s) > 4 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// paramValue extracts one key's value from a raw query string,
// ignoring every other param (unknown params are never a failure).
func paramValue(rawParams, key string) string {
	for rawParams != "" {
		var pair string
		pair, rawParams, _ = strings.Cut(rawParams, "&")
		if k, v, ok := strings.Cut(pair, "="); ok && k == key {
			return v
		}
	}
	return ""
}
