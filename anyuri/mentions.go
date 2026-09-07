package anyuri

// ExtractMentions scans text for mention URIs (any://m/<spaceId>/<identity>)
// and returns the mentioned identities, deduplicated, in first-occurrence
// order. It is a substring scan, not a markdown parser: it works
// identically on raw text and on CommonMark link destinations
// ("[Zarko](any://m/<sp>/<id>)" — an unescaped ")" legally terminates a
// CommonMark destination, and ids are base58 so no terminator byte can
// appear inside one). Percent-encoded destinations are not decoded; ids
// never need encoding. This is the sanctioned mention scanner — the
// chat mentions derivation and any client-side detection share it.
// A filter over ExtractLinks, the general scanner.
func ExtractMentions(text string) []string {
	var out []string
	var seen map[string]struct{}
	for _, u := range ExtractLinks(text) {
		if u.Kind != KindMention || u.Identity == "" {
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

// isTailByte reports whether c may appear in an any:// token AFTER
// the "any://m/" prefix: alphanumerics (base58 ids) plus the
// structural bytes of the grammar (path, params, fragment), '.'
// (space ids are "<cid>.<replKey>") and "_"/"-" (fragments like
// turn_3, variant tags). Deliberately NOT ':' — only the
// already-matched scheme needs it, so a glued colon terminates the
// token instead of corrupting the identity; glued periods are shed by
// the identity-segment cut in ExtractMentions.
func isTailByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '/', '?', '&', '=', '#', '_', '-', '.':
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
