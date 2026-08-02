package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestServer_MarkdownEdit drives PATCH .../editor/markdown through
// echo against a real SDK: the checklist-tick scenario (single-block
// style update, ids stable), replaceAll, a block-splitting edit, and
// the idempotent no-op.
func TestServer_MarkdownEdit(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	// Seed: heading + two-item checklist.
	rec := doJSON(t, e, http.MethodPost, base+"/editor/markdown/append",
		`{"content":"# Books\n\n- [ ] Children of Time\n\n- [ ] Blindsight"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("append: %d %s", rec.Code, rec.Body.String())
	}
	seeded := blocksList(t, e, base)
	if len(seeded.Records) != 3 {
		t.Fatalf("seed: %d blocks, want 3", len(seeded.Records))
	}

	// 1. Tick the first checkbox — must land as ONE updated block,
	// nothing inserted or deleted, ids stable.
	resp := markdownEdit(t, e, base,
		`{"edits":[{"oldText":"- [ ] Children of Time","newText":"- [x] Children of Time"}]}`)
	if len(resp.Updated) != 1 || len(resp.Inserted) != 0 || len(resp.Deleted) != 0 {
		t.Fatalf("tick: %+v, want exactly one update", resp)
	}
	if resp.Unchanged != 2 {
		t.Errorf("tick: unchanged = %d, want 2", resp.Unchanged)
	}
	after := blocksList(t, e, base)
	if len(after.Records) != 3 {
		t.Fatalf("tick: %d blocks after, want 3", len(after.Records))
	}
	for i, b := range after.Records {
		if b.Id != seeded.Records[i].Id {
			t.Errorf("tick: block %d id changed %s → %s", i, seeded.Records[i].Id, b.Id)
		}
	}
	ticked := after.Records[1]
	if ticked.Id != resp.Updated[0] {
		t.Errorf("tick: updated id %s, want %s", resp.Updated[0], ticked.Id)
	}
	if ticked.Type != "check_list_item" || ticked.Style["checked"] != true {
		t.Errorf("tick: type=%q style=%v, want check_list_item checked=true", ticked.Type, ticked.Style)
	}
	if got := getMarkdown(t, e, base+"/editor/markdown"); got !=
		"# Books\n\n- [x] Children of Time\n\n- [ ] Blindsight" {
		t.Errorf("tick: markdown = %q", got)
	}

	// 2. replaceAll across blocks.
	resp = markdownEdit(t, e, base,
		`{"edits":[{"oldText":"- [","newText":"- [","replaceAll":true},{"oldText":"# Books","newText":"# Books read"}]}`)
	if len(resp.Updated) != 1 {
		t.Fatalf("replaceAll: %+v, want the heading updated", resp)
	}
	resp = markdownEdit(t, e, base,
		`{"edits":[{"oldText":"] ","newText":"] the ","replaceAll":true}]}`)
	if len(resp.Updated) != 2 {
		t.Fatalf("replaceAll: %+v, want both checklist items updated", resp)
	}
	if got := getMarkdown(t, e, base+"/editor/markdown"); got !=
		"# Books read\n\n- [x] the Children of Time\n\n- [ ] the Blindsight" {
		t.Errorf("replaceAll: markdown = %q", got)
	}

	// 3. Fuzzy fallback: curly quotes + em dash + trailing spaces in
	// the quoted line still hit the ASCII original.
	resp = markdownEdit(t, e, base,
		`{"edits":[{"oldText":"# Books read   ","newText":"# Books — read"}]}`)
	if len(resp.Updated) != 1 {
		t.Fatalf("fuzzy: %+v, want one update", resp)
	}

	// 4. An edit whose replacement spans a block boundary splits into
	// insert + update through the diff.
	resp = markdownEdit(t, e, base,
		`{"edits":[{"oldText":"- [ ] the Blindsight","newText":"- [ ] the Blindsight\n\nfootnote paragraph"}]}`)
	if len(resp.Inserted) != 1 || len(resp.Deleted) != 0 {
		t.Fatalf("split: %+v, want one insert", resp)
	}

	// 5. No-op edit: identical replacement → 200, nothing written.
	resp = markdownEdit(t, e, base,
		`{"edits":[{"oldText":"footnote paragraph","newText":"footnote paragraph"}]}`)
	if len(resp.Inserted)+len(resp.Updated)+len(resp.Deleted) != 0 {
		t.Fatalf("noop: %+v, want no ops", resp)
	}
	if resp.Unchanged != 4 {
		t.Errorf("noop: unchanged = %d, want 4", resp.Unchanged)
	}
}

// TestServer_MarkdownEdit_Errors pins the typed 400s and that a
// failing batch writes nothing.
func TestServer_MarkdownEdit_Errors(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId

	rec := doJSON(t, e, http.MethodPost, base+"/editor/markdown/append",
		`{"content":"alpha\n\ndup\n\ndup"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("append: %d %s", rec.Code, rec.Body.String())
	}
	before := getMarkdown(t, e, base+"/editor/markdown")

	cases := []struct {
		name string
		body string
		code string
	}{
		{"no_match", `{"edits":[{"oldText":"never there","newText":"x"}]}`, "markdown.no_match"},
		{"ambiguous", `{"edits":[{"oldText":"dup","newText":"x"}]}`, "markdown.ambiguous_match"},
		{"overlap", `{"edits":[{"oldText":"alpha\n\ndup","newText":"x"},{"oldText":"alpha","newText":"y"}]}`, "markdown.overlapping_edits"},
		{"batch_aborts_on_one_bad", `{"edits":[{"oldText":"alpha","newText":"CHANGED"},{"oldText":"never there","newText":"x"}]}`, "markdown.no_match"},
		{"empty_edits", `{"edits":[]}`, "request.missing_field"},
		{"empty_old_text", `{"edits":[{"oldText":"","newText":"x"}]}`, "request.missing_field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, e, http.MethodPatch, base+"/editor/markdown", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: code %d %s", tc.name, rec.Code, rec.Body.String())
			}
			var env struct {
				Error struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if env.Error.Code != tc.code {
				t.Errorf("%s: error code %q, want %q", tc.name, env.Error.Code, tc.code)
			}
		})
	}

	// Ambiguity details carry the occurrence count.
	rec = doJSON(t, e, http.MethodPatch, base+"/editor/markdown",
		`{"edits":[{"oldText":"dup","newText":"x"}]}`)
	var env struct {
		Error struct {
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Details["occurrences"] != float64(2) {
		t.Errorf("ambiguous details = %v, want occurrences 2", env.Error.Details)
	}

	// Nothing was written by any of the failing requests.
	if after := getMarkdown(t, e, base+"/editor/markdown"); after != before {
		t.Errorf("document changed by failing edits: %q → %q", before, after)
	}
}

// mdEditResp is the test-local decode target for the shared
// set-response shape.
type mdEditResp struct {
	Inserted  []string `json:"inserted"`
	Updated   []string `json:"updated"`
	Deleted   []string `json:"deleted"`
	Unchanged int      `json:"unchanged"`
}

// markdownEdit PATCHes .../editor/markdown and decodes the response.
func markdownEdit(t *testing.T, e http.Handler, base, body string) (out mdEditResp) {
	t.Helper()
	rec := doJSON(t, e, http.MethodPatch, base+"/editor/markdown", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH markdown %s: %d %s", body, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode edit response: %v", err)
	}
	return out
}
