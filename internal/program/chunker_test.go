package program

import (
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
)

func TestMethodData(t *testing.T) {
	arena := &anyenc.Arena{}
	rec := func(name, text string) *anyenc.Value {
		v := arena.NewObject()
		if name != "" {
			v.Set(fieldName, arena.NewString(name))
		}
		if text != "" {
			v.Set(fieldText, arena.NewString(text))
		}
		return v
	}

	cases := []struct{ name, text, want string }{
		{"doStuff(x)", "performs the zorble operation", "doStuff(x)\nperforms the zorble operation"},
		{"doStuff(x)", "", "doStuff(x)"},
		{"", "body only", "body only"},
		{"", "", ""},
		{"  trimmed  ", "  body  ", "trimmed\nbody"},
	}
	for _, c := range cases {
		if got := methodData(rec(c.name, c.text)); got != c.want {
			t.Errorf("methodData(name=%q text=%q) = %q, want %q", c.name, c.text, got, c.want)
		}
	}
}
