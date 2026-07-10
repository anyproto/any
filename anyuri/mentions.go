package anyuri

import "strings"

// mentionPrefix is what ExtractMentions scans for.
const mentionPrefix = prefix + string(KindMention) + "/"

// ExtractMentions scans text for mention URIs (any://m/<spaceId>/<identity>)
// and returns the mentioned identities, deduplicated, in first-occurrence
// order. It is a substring scan, not a markdown parser: it works
// identically on raw text and on CommonMark link destinations
// ("[Zarko](any://m/<sp>/<id>)" — an unescaped ")" legally terminates a
// CommonMark destination, and ids are base58 so no terminator byte can
// appear inside one). Percent-encoded destinations are not decoded; ids
// never need encoding. This is the sanctioned mention scanner — the
// chat mentions derivation and any client-side detection share it.
func ExtractMentions(text string) []string {
	var out []string
	var seen map[string]struct{}
	for i := 0; i < len(text); {
		idx := strings.Index(text[i:], mentionPrefix)
		if idx < 0 {
			break
		}
		start := i + idx
		// Resume after the matched prefix regardless of the token's
		// fate — safe against crafted tokens containing the prefix.
		i = start + len(mentionPrefix)
		// A scheme hit mid-word ("many://m/…") is another scheme's
		// URI, not ours.
		if start > 0 && isSchemeByte(text[start-1]) {
			continue
		}
		end := start
		for end < len(text) && isURIByte(text[end]) {
			end++
		}
		token := strings.TrimRight(text[start:end], ".,;:!?")
		u, err := Parse(token)
		if err != nil || u.Kind != KindMention || u.Identity == "" {
			continue
		}
		if _, dup := seen[u.Identity]; dup {
			continue
		}
		if seen == nil {
			seen = make(map[string]struct{})
		}
		seen[u.Identity] = struct{}{}
		out = append(out, u.Identity)
	}
	return out
}

// isURIByte reports whether c may appear inside an any:// token:
// alphanumerics (base58 ids, dataset names) plus the structural bytes
// of the grammar (path, params, fragment) and "_"/"-"/"." seen in
// dataset names, fragments and variant tags.
func isURIByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case ':', '/', '?', '&', '=', '#', '_', '-', '.':
		return true
	}
	return false
}

// isSchemeByte reports whether c is valid inside a URI scheme
// (RFC 3986): if it immediately precedes "any://", the match belongs
// to a longer scheme.
func isSchemeByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return c == '+' || c == '-' || c == '.'
}
