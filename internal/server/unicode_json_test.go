package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-sync-sdk/space"
)

// These are valid stored strings. Go quoting can emit \\U, \\x, \\a or \\v
// escapes for them, which JSON readers reject and which abort an entire window.
func TestQueryUnicodeJSON(t *testing.T) {
	cases := map[string]string{
		"flag-tags":            "Scotland: 🏴\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f\nquoted \"text\" \\",
		"supplementary-format": "line\n\U000e0001\U000e0067",
		"unicode":              "日本語 café Привет 😀\n\u2028\u2029\u007f",
	}
	for c := rune(0); c < 32; c++ {
		cases[fmt.Sprintf("control-%02x", c)] = "before\n" + string(c) + "\U000e0067after"
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			input, err := json.Marshal(map[string]any{"id": "unicode-record", "payload": map[string]string{text: text}})
			if err != nil {
				t.Fatal(err)
			}
			doc, err := anyenc.ParseJson(string(input))
			if err != nil {
				t.Fatal(err)
			}
			result := &space.QueryResult{Initial: []*anyenc.Value{doc}}
			shaper := recordShaper{}
			t.Run("HTTP", func(t *testing.T) {
				ctx, rec := newEchoCtx()
				if err := writeQueryResponse(ctx, result, false, shaper); err != nil {
					t.Fatal(err)
				}
				assertUnicodeJSON(t, rec.Body.Bytes(), text)
			})
			t.Run("snapshot", func(t *testing.T) {
				rec := httptest.NewRecorder()
				snapshot, err := encodeSnapshot(result, false, shaper)
				if err != nil {
					t.Fatal(err)
				}
				if err := writeSSEData(rec, "snapshot", "", snapshot); err != nil {
					t.Fatal(err)
				}
				assertUnicodeSSE(t, rec.Body.String(), text)
			})
			t.Run("changes", func(t *testing.T) {
				rec := httptest.NewRecorder()
				events := []space.SubscriptionEvent{{Updated: []space.SubRecord{{Id: "unicode-record", Doc: doc}}}}
				if err := writeQuerySubscribeBatch(rec, events, shaper); err != nil {
					t.Fatal(err)
				}
				assertUnicodeSSE(t, rec.Body.String(), text)
			})
			t.Run("ops", func(t *testing.T) {
				rec := httptest.NewRecorder()
				events := []space.SubscriptionEvent{{Updated: []space.SubRecord{{
					Id:  "unicode-record",
					Ops: []space.EventOp{{Type: "$set", Payload: doc}},
				}}}}
				if err := writeQuerySubscribeBatch(rec, events, shaper); err != nil {
					t.Fatal(err)
				}
				assertUnicodeSSE(t, rec.Body.String(), text)
			})
		})
	}
}

func assertUnicodeSSE(t *testing.T, frame, text string) {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			assertUnicodeJSON(t, []byte(data), text)
			return
		}
	}
	t.Fatalf("missing SSE data frame: %q", frame)
}

func assertUnicodeJSON(t *testing.T, wire []byte, text string) {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	var find func(any) bool
	find = func(value any) bool {
		switch value := value.(type) {
		case map[string]any:
			if got, ok := value[text]; ok {
				return got == text
			}
			for _, child := range value {
				if find(child) {
					return true
				}
			}
		case []any:
			for _, child := range value {
				if find(child) {
					return true
				}
			}
		}
		return false
	}
	if !find(decoded) {
		t.Fatalf("JSON did not preserve field name and value %q", text)
	}
}
