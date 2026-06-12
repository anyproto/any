package index

// ValidScope reports whether s is an acceptable scope slug: 1..64
// chars of [a-z0-9_-]. Scopes are an open set — property meta flags
// carry arbitrary slugs — so the search handler and the prop-chunker
// catalog share this sanity rule instead of a fixed list.
func ValidScope(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}
