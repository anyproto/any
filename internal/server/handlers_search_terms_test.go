package server

// Deliberately TAG-FREE: the shipped build sets `fts vector` and CI runs
// the whole suite with them, so a test gated on `!fts` would never
// execute. The predicate takes the capability as an argument for exactly
// that reason — both builds are covered from either one.

import (
	"testing"

	"github.com/anyproto/any/internal/api"
)

func TestTermFilterUnsupported(t *testing.T) {
	cases := []struct {
		name string
		fts  bool
		req  api.SearchRequest
		want bool
	}{
		{"no terms, no fts", false, api.SearchRequest{Query: "zeppelin"}, false},
		{"require without fts", false, api.SearchRequest{Query: "zeppelin", Require: []string{"1937"}}, true},
		{"exclude without fts", false, api.SearchRequest{Query: "zeppelin", Exclude: []string{"lunch"}}, true},
		{"require with fts", true, api.SearchRequest{Query: "zeppelin", Require: []string{"1937"}}, false},
		{"exclude with fts", true, api.SearchRequest{Query: "zeppelin", Exclude: []string{"lunch"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := termFilterUnsupported(tc.fts, &tc.req); got != tc.want {
				t.Errorf("termFilterUnsupported(%v, %+v) = %v, want %v", tc.fts, tc.req, got, tc.want)
			}
		})
	}
}
