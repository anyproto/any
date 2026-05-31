//go:build integration

// Integration test: prove the `any` server stores program source/description
// byte-for-byte, with no escaping, transformation, or markdown mangling
// anywhere in the create -> /modify -> storage -> /query pipeline.
//
// Background: the agent (bobrik-watch) authors program source by building a JS
// string (often a backtick template literal) and handing it to
// `anyPrograms.createProgram`, which POSTs `{code: source}` to /modify. A long
// debugging session showed regexes like `[\s\S]` arriving mangled. We traced
// that to *standard ECMAScript* string-literal semantics inside the JS engine
// (`"\s"` === `"s"`), NOT to a custom escaping pass — see the sibling
// engine_string_semantics probe. This test pins the *other* half of that claim:
// once a string reaches the server, the server must round-trip it unchanged.
// If this test ever fails, the server grew a quirk and we should remove it.
//
// Run against a live server (one account, one data dir):
//
//	go build ./cmd/any && ANY_DATA_DIR=/tmp/any-it ./any init
//	ANY_DATA_DIR=/tmp/any-it ./any run &            # foreground server on :7001
//	go test -tags integration ./cmd/bobrik-watch/tests/ -run Program -v
//
// Override the target with ANY_ADDR (default http://127.0.0.1:7001).
package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const spaceName = "integration_test"

func baseURL() string {
	if v := os.Getenv("ANY_ADDR"); v != "" {
		if !strings.HasPrefix(v, "http") {
			return "http://" + v
		}
		return v
	}
	return "http://127.0.0.1:7001"
}

// doJSON performs an HTTP request against the server and returns status + body.
// It t.Skips the whole test if the server is unreachable, so the suite is inert
// when no server is running rather than failing spuriously.
func doJSON(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, baseURL()+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Skipf("server unreachable at %s (%v) — start it with `any run`", baseURL(), err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// ensureSpace finds the active integration_test space or creates it.
func ensureSpace(t *testing.T) string {
	t.Helper()
	st, body := doJSON(t, http.MethodGet, "/v1/spaces", nil)
	if st != http.StatusOK {
		t.Fatalf("GET /v1/spaces: %d %s", st, body)
	}
	var list struct {
		Spaces []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"spaces"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode space list: %v", err)
	}
	for _, s := range list.Spaces {
		if s.Name == spaceName && s.Status == "active" {
			return s.ID
		}
	}
	st, body = doJSON(t, http.MethodPost, "/v1/spaces", map[string]string{"name": spaceName})
	if st != http.StatusCreated {
		t.Fatalf("create space: %d %s", st, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created space: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created space has empty id: %s", body)
	}
	t.Logf("created space %s = %s", spaceName, created.ID)
	return created.ID
}

// createProgramObject creates an empty `program`-typed object and returns its id.
func createProgramObject(t *testing.T, spaceID, name string) string {
	t.Helper()
	st, body := doJSON(t, http.MethodPost, "/v1/spaces/"+spaceID+"/objects", map[string]any{
		"types": []string{"program"},
		"initialProperties": map[string]any{
			"any":     map[string]any{"name": name},
			"program": map[string]any{"name": name, "version": "v1"},
		},
	})
	if st != http.StatusCreated {
		t.Fatalf("create object: %d %s", st, body)
	}
	var created struct {
		ObjectID string `json:"objectId"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode created object: %v", err)
	}
	if created.ObjectID == "" {
		t.Fatalf("created object has empty id: %s", body)
	}
	return created.ObjectID
}

// writeField does a $set of {field: value} on dataset/record "main".
func writeField(t *testing.T, spaceID, objectID, dataset, field, value string) {
	t.Helper()
	st, body := doJSON(t, http.MethodPost, "/v1/spaces/"+spaceID+"/modify", map[string]any{
		"objectId": objectID,
		"dataset":  dataset,
		"records": []any{map[string]any{
			"id":     "main",
			"upsert": true,
			"ops":    []any{map[string]any{"type": "$set", "path": "", "value": map[string]any{field: value}}},
		}},
	})
	if st != http.StatusOK {
		t.Fatalf("modify %s: %d %s", dataset, st, body)
	}
}

// readField reads {field} from dataset record "main".
func readField(t *testing.T, spaceID, objectID, dataset, field string) string {
	t.Helper()
	st, body := doJSON(t, http.MethodPost, "/v1/spaces/"+spaceID+"/query", map[string]any{
		"objectId": objectID,
		"dataset":  dataset,
	})
	if st != http.StatusOK {
		t.Fatalf("query %s: %d %s", dataset, st, body)
	}
	var out struct {
		Records []map[string]json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode query %s: %v", dataset, err)
	}
	if len(out.Records) == 0 {
		t.Fatalf("query %s: no records", dataset)
	}
	raw, ok := out.Records[0][field]
	if !ok {
		t.Fatalf("query %s: record has no %q field: %v", dataset, field, out.Records[0])
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode %s.%s: %v", dataset, field, err)
	}
	return s
}

// assertExact reports a precise diff (first divergent byte) on mismatch.
func assertExact(t *testing.T, label, want, got string) {
	t.Helper()
	if want == got {
		return
	}
	n := len(want)
	if len(got) < n {
		n = len(got)
	}
	at := n
	for i := 0; i < n; i++ {
		if want[i] != got[i] {
			at = i
			break
		}
	}
	lo := at - 20
	if lo < 0 {
		lo = 0
	}
	t.Errorf("%s: round-trip MISMATCH (len want=%d got=%d, first diff at byte %d)\n  want…%q…\n   got…%q…",
		label, len(want), len(got), at,
		snippet(want, lo, at+20), snippet(got, lo, at+20))
}

func snippet(s string, lo, hi int) string {
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return s[lo:hi]
}

// torture cases: every one of these is a string the agent could legitimately
// hand to createProgram. The server must give it back byte-identical.
var cases = []struct {
	name string
	src  string
}{
	{"regex_charclass", `var re = /<item[^>]*>([\s\S]*?)<\/item>/g; // the techcrunch case`},
	{"cdata_brackets", `var d = /<description>(?:<!\[CDATA\[)?([\s\S]*?)(?:\]\]>)?<\/description>/;`},
	{"literal_backslash_s", `a\s\S\d\w b`}, // must stay 8 backslash-pairs, NOT collapse
	{"double_backslash", `path = "C:\\Users\\x"; nl = "\\n";`},
	{"all_quote_kinds", "single ' double \" backtick ` mixed \"'`"},
	{"newlines_tabs", "line1\n\tindented\r\nwindows\n"},
	{"unicode", "em–dash, curly ‘quote’ “quote”, emoji 🦫, math ∑∫"},
	{"json_inside", `var x = {"a": 1, "b": "he said \"hi\""};`},
	{"template_and_dollar", "var t = `sum=${a+b}`; var p = \"$PATH\";"},
	{"markdown_fence", "## Tool Description\n```js\nvar r = /\\d+/;\n```\n- bullet\n> quote\n"},
	{"control_and_slashes", "a/b\\c//d\\\\e\t\\t"},
	{"empty", ""},
}

func TestProgramSourceRoundTrip(t *testing.T) {
	spaceID := ensureSpace(t)
	for i, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			obj := createProgramObject(t, spaceID, fmt.Sprintf("rt_src_%d_%s", i, tc.name))
			writeField(t, spaceID, obj, "program_source", "code", tc.src)
			got := readField(t, spaceID, obj, "program_source", "code")
			assertExact(t, "program_source.code", tc.src, got)
		})
	}
}

func TestProgramDescriptionRoundTrip(t *testing.T) {
	spaceID := ensureSpace(t)
	for i, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			obj := createProgramObject(t, spaceID, fmt.Sprintf("rt_desc_%d_%s", i, tc.name))
			writeField(t, spaceID, obj, "program_description", "text", tc.src)
			got := readField(t, spaceID, obj, "program_description", "text")
			assertExact(t, "program_description.text", tc.src, got)
		})
	}
}
