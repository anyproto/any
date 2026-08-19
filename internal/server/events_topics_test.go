package server

import (
	"reflect"
	"testing"
)

func TestEventTopic(t *testing.T) {
	cases := []struct {
		typ, target, acc, want string
	}{
		{"process.progress", "p1", "A", "ev/process/progress/p1"},
		{"process.progress", "", "A", "ev/process/progress/-"},
		{"ping", "x", "A", "ev/ping/x"},
		{"editor.cursor", "obj1", "AccId", "acc/ev/editor/cursor/obj1/AccId"},
		{"editor.cursor", "", "AccId", "acc/ev/editor/cursor/-/AccId"},
	}
	for _, tc := range cases {
		if got := eventTopic(tc.typ, tc.target, tc.acc); got != tc.want {
			t.Errorf("eventTopic(%q,%q,%q) = %q, want %q", tc.typ, tc.target, tc.acc, got, tc.want)
		}
	}
}

func TestFilterPatterns(t *testing.T) {
	cases := []struct {
		name string
		f    eventFilter
		want []string
	}{
		{"catch-all", eventFilter{}, []string{"ev/>", "acc/ev/>"}},
		{"prefix", eventFilter{types: []string{"process.*"}}, []string{"ev/process/>"}},
		{"prefix covering self-owned", eventFilter{types: []string{"editor.*"}},
			[]string{"ev/editor/>", "acc/ev/editor/>"}},
		{"exact no target", eventFilter{types: []string{"process.progress"}},
			[]string{"ev/process/progress/*"}},
		{"exact with targets", eventFilter{types: []string{"process.progress"}, targets: []string{"a", "b"}},
			[]string{"ev/process/progress/a", "ev/process/progress/b"}},
		{"exact self-owned", eventFilter{types: []string{"editor.cursor"}},
			[]string{"acc/ev/editor/cursor/>"}},
		{"mixed dedup", eventFilter{types: []string{"ui.*", "ui.*"}}, []string{"ev/ui/>"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filterPatterns(&tc.f); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("filterPatterns = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPatternSubsumes(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"ev/>", "ev/process/>", true},
		{"ev/>", "ev/process/progress/*", true},
		{"ev/>", "ev/a", true},
		{"ev/process/>", "ev/process/progress/p1", true},
		{"ev/process/>", "ev/other/>", false},
		{"ev/a/*", "ev/a/x", true},
		{"ev/a/*", "ev/a/>", false},
		{"ev/a/*", "ev/a/x/y", false},
		{"ev/a/x", "ev/a/*", false},
		{"ev/a", "ev/a", true},
		{"acc/ev/>", "ev/>", false},
	}
	for _, tc := range cases {
		if got := patternSubsumes(tc.a, tc.b); got != tc.want {
			t.Errorf("patternSubsumes(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMaximalPatterns(t *testing.T) {
	set := map[string]int{
		"ev/process/>":           1,
		"ev/process/progress/p1": 2, // subsumed
		"ev/ui/open_space/*":     1,
		"acc/ev/editor/cursor/>": 1,
	}
	want := []string{"acc/ev/editor/cursor/>", "ev/process/>", "ev/ui/open_space/*"}
	if got := maximalPatterns(set); !reflect.DeepEqual(got, want) {
		t.Errorf("maximalPatterns = %v, want %v", got, want)
	}
}
