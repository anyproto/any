package api

import "strings"

// MetaKeyMaxBytes bounds a meta key on a type or a collection.
const MetaKeyMaxBytes = 64

// CheckMetaEntry validates one entry of a definition's meta bag: a
// single-level key (no '.', no '$', at most MetaKeyMaxBytes) and a
// scalar value — string, bool or number. nilOK admits a nil value,
// the clear on PATCH. Returns "" when the entry is fine, otherwise the
// reason, worded for the caller's error message.
func CheckMetaEntry(key string, v any, nilOK bool) string {
	if key == "" || len(key) > MetaKeyMaxBytes || strings.ContainsAny(key, ".$") {
		return "meta keys are single-level: no '.', no '$', at most 64 bytes"
	}
	switch v.(type) {
	case string, bool, float64, int, int64:
		return ""
	case nil:
		if nilOK {
			return ""
		}
	}
	return "meta values are strings, booleans or numbers"
}
