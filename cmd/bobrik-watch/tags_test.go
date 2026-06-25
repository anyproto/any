package main

import (
	"reflect"
	"testing"
)

func TestParseTags(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   []string
	}{
		{
			name:   "marker on second line",
			source: "// __main_source\n// __tags: integration\n// desc\nimport x from \"y\";",
			want:   []string{"integration"},
		},
		{
			name:   "multiple tags trimmed and deduped",
			source: "// __main_source\n// __tags: integration ,  crm , integration\nvar a = 1;",
			want:   []string{"integration", "crm"},
		},
		{
			name:   "no marker",
			source: "// __main_source\n// just a normal program\nvar a = 1;",
			want:   nil,
		},
		{
			name:   "marker after code does not count",
			source: "// __main_source\nvar a = 1;\n// __tags: integration",
			want:   nil,
		},
		{
			name:   "marker inside a string is not the header marker",
			source: "// __main_source\nvar s = \"// __tags: nope\";",
			want:   nil,
		},
		{
			name:   "blank lines in header are skipped",
			source: "// __main_source\n\n// __tags: integration\n\nvar a = 1;",
			want:   []string{"integration"},
		},
		{
			name:   "empty tag list yields nil",
			source: "// __main_source\n// __tags:   \nvar a = 1;",
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTags(tc.source)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("parseTags() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
