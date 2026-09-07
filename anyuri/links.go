package anyuri

import "strings"

// ExtractLinks scans text for any:// URIs of every known kind and
// returns them parsed, deduplicated by their text form, in
// first-occurrence order. Unknown kinds and malformed tokens are
// skipped. Like ExtractMentions it is a substring scan, not a markdown
// parser: it finds raw tokens and CommonMark link destinations alike
// (an unescaped ")" legally terminates a destination, and ids are
// base58, so no terminator byte can appear inside one). A glued
// sentence period after the last id segment is shed ("…/<id>.Next").
// Percent-encoding is not decoded: '%' ends a token, so an encoded id
// truncates rather than resolving — ids never need encoding.
//
// This is the sanctioned link scanner: the server's link index and
// clients detecting links in text share it.
func ExtractLinks(text string) []URI {
	var out []URI
	var seen map[string]struct{}
	for i := 0; i < len(text); {
		idx := strings.Index(text[i:], prefix)
		if idx < 0 {
			break
		}
		start := i + idx
		// Resume after the matched prefix regardless of the token's
		// fate — safe against crafted tokens containing the prefix.
		i = start + len(prefix)
		// A scheme hit mid-word ("many://…") is another scheme's URI.
		if start > 0 && isSchemeByte(text[start-1]) {
			continue
		}
		// The prefix is already matched; scan only the TAIL charset
		// from here ('.' stays — space ids are "<cid>.<replKey>" —
		// ':' is only ever in the scheme, so a glued colon terminates).
		end := i
		for end < len(text) && isTailByte(text[end]) {
			end++
		}
		token := strings.TrimRight(text[start:end], ".,;:!?")
		u, err := Parse(token)
		if err != nil {
			continue // malformed, or a kind this build does not know: not ours
		}
		if !shedGluedPeriod(&u) {
			continue
		}
		key := u.String()
		if _, dup := seen[key]; dup {
			continue
		}
		if seen == nil {
			seen = make(map[string]struct{})
		}
		seen[key] = struct{}{}
		out = append(out, u)
	}
	return out
}

// shedGluedPeriod cuts a glued sentence period off the URI's LAST id
// segment: ids (object, record, identity, property, file) are base58
// and never dotted, so a '.' inside one is the absorbed next word
// ("…/<id>.Check this"). Space ids are dotted by construction and are
// never the last segment of a typed form. Reports false when the cut
// empties the segment.
func shedGluedPeriod(u *URI) bool {
	cut := func(s *string) bool {
		if dot := strings.IndexByte(*s, '.'); dot >= 0 {
			*s = (*s)[:dot]
		}
		return *s != ""
	}
	switch u.Kind {
	case KindObject:
		if u.RecordId != "" {
			return cut(&u.RecordId)
		}
		return cut(&u.ObjectId)
	case KindMention:
		return cut(&u.Identity)
	case KindProp:
		return cut(&u.PropId)
	case KindFile:
		return cut(&u.FileId)
	}
	return true
}

// Canonical returns the index-key form of u: the typed global shape
// with the fragment and every param dropped, a legacy in-space
// reference resolved against spaceId. Returns false for a URI that is
// not a link target — a space reference, an unresolvable in-space
// form (no spaceId in hand), an empty id.
//
//	any://<id>, any://<sp>/<id>, any://o/<sp>/<id>   → any://o/<sp>/<id>
//	any://o/<sp>/<id>/<ds>/<rec>                   → unchanged
//	any://p/<sp>/<id>/<prop>                        → unchanged
//	any://m/<sp>/<identity>                          → unchanged
//	any://f/<sp>/<file>[?variant=…]                  → any://f/<sp>/<file>
//	any://s/<sp>                                     → not a target
func (u URI) Canonical(spaceId string) (URI, bool) {
	c := URI{Kind: u.Kind, SpaceId: u.SpaceId}
	if c.SpaceId == "" {
		c.SpaceId = spaceId
	}
	if c.SpaceId == "" {
		return URI{}, false
	}
	switch u.Kind {
	case KindObject:
		if u.ObjectId == "" {
			return URI{}, false
		}
		c.ObjectId, c.Dataset, c.RecordId = u.ObjectId, u.Dataset, u.RecordId
	case KindProp:
		if u.ObjectId == "" || u.PropId == "" {
			return URI{}, false
		}
		c.ObjectId, c.PropId = u.ObjectId, u.PropId
	case KindMention:
		if u.Identity == "" {
			return URI{}, false
		}
		c.Identity = u.Identity
	case KindFile:
		if u.FileId == "" {
			return URI{}, false
		}
		c.FileId = u.FileId
	default:
		return URI{}, false
	}
	return c, true
}

// ObjectKey returns the canonical object reference a target belongs
// to — "any://o/<sp>/<id>" for an object, one of its records or one
// of its property values — and false for targets that are not objects
// (identities, files). Callers key "who links this object, in any of
// its parts" on it.
func (u URI) ObjectKey() (string, bool) {
	switch u.Kind {
	case KindObject, KindProp:
		if u.SpaceId == "" || u.ObjectId == "" {
			return "", false
		}
		return BuildObjectGlobal(u.SpaceId, u.ObjectId), true
	}
	return "", false
}

// IsPart reports whether a canonical target names a part of an object
// — a dataset record or a property value — rather than the object.
func (u URI) IsPart() bool {
	switch u.Kind {
	case KindObject:
		return u.RecordId != ""
	case KindProp:
		return true
	}
	return false
}
