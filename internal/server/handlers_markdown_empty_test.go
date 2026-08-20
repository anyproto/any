package server

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestServer_MarkdownEmptyParagraphs pins the wire contract clients
// encode against: blank lines beyond the one separating two blocks
// are empty paragraph records, they survive PUT → GET byte-for-byte,
// and re-saving the same document writes nothing.
func TestServer_MarkdownEmptyParagraphs(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	md := base + "/editor/markdown"

	// One empty paragraph between two paragraphs.
	resp := markdownSet(t, e, md, "alpha\n\n\nbeta")
	if len(resp.Inserted) != 3 {
		t.Fatalf("set: inserted %v, want 3 blocks", resp.Inserted)
	}
	if got := getMarkdown(t, e, md); got != "alpha\n\n\nbeta" {
		t.Fatalf("get: %q, want %q", got, "alpha\n\n\nbeta")
	}
	seeded := blocksList(t, e, base)
	if len(seeded.Records) != 3 {
		t.Fatalf("blocks: %d, want 3", len(seeded.Records))
	}
	if mid := seeded.Records[1]; mid.Type != "paragraph" || mid.Text != "" {
		t.Errorf("middle block: type=%q text=%q, want an empty paragraph", mid.Type, mid.Text)
	}

	// Re-saving the canonical rendering must be a pure no-op — this is
	// what keeps a client from looping save → canonical echo → save.
	again := markdownSet(t, e, md, "alpha\n\n\nbeta")
	if again.Unchanged != 3 || len(again.Inserted) != 0 || len(again.Updated) != 0 || len(again.Deleted) != 0 {
		t.Fatalf("resave: %+v, want unchanged=3 and no writes", again)
	}

	// Deleting the empty paragraph removes exactly that record.
	shrunk := markdownSet(t, e, md, "alpha\n\nbeta")
	if len(shrunk.Deleted) != 1 || shrunk.Unchanged != 2 || len(shrunk.Inserted) != 0 {
		t.Fatalf("shrink: %+v, want one delete", shrunk)
	}
	if shrunk.Deleted[0] != seeded.Records[1].Id {
		t.Errorf("shrink: deleted %s, want the empty paragraph %s", shrunk.Deleted[0], seeded.Records[1].Id)
	}
	after := blocksList(t, e, base)
	if len(after.Records) != 2 {
		t.Fatalf("shrink: %d blocks, want 2", len(after.Records))
	}
	if after.Records[0].Id != seeded.Records[0].Id || after.Records[1].Id != seeded.Records[2].Id {
		t.Errorf("shrink: surviving ids churned: %s,%s", after.Records[0].Id, after.Records[1].Id)
	}
	if got := getMarkdown(t, e, md); got != "alpha\n\nbeta" {
		t.Errorf("shrink: markdown = %q", got)
	}
}

// TestServer_MarkdownEmptyParagraphEdges covers the document edges,
// where a blank run has no separator duty at all, and the append
// fast path, which treats edge blank lines as framing.
func TestServer_MarkdownEmptyParagraphEdges(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	md := base + "/editor/markdown"

	// A single trailing newline is still just a terminator.
	markdownSet(t, e, md, "alpha\n")
	if got := getMarkdown(t, e, md); got != "alpha" {
		t.Fatalf("terminator: %q, want %q", got, "alpha")
	}
	if n := len(blocksList(t, e, base).Records); n != 1 {
		t.Fatalf("terminator: %d blocks, want 1", n)
	}

	// Leading and trailing empty paragraphs round-trip.
	markdownSet(t, e, md, "\nalpha\n\n")
	if got := getMarkdown(t, e, md); got != "\nalpha\n\n" {
		t.Fatalf("edges: %q, want %q", got, "\nalpha\n\n")
	}
	blocks := blocksList(t, e, base)
	if len(blocks.Records) != 3 {
		t.Fatalf("edges: %d blocks, want 3", len(blocks.Records))
	}
	if blocks.Records[0].Text != "" || blocks.Records[2].Text != "" {
		t.Errorf("edges: want empty paragraphs around alpha, got %q / %q",
			blocks.Records[0].Text, blocks.Records[2].Text)
	}

	// Append positions its fragment itself, so blank lines wrapping it
	// are framing — only the fragment's own blocks are created.
	rec := doJSON(t, e, http.MethodPost, md+"/append", `{"content":"\n\nbeta\n\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("append: %d %s", rec.Code, rec.Body.String())
	}
	var appended mdEditResp
	if err := json.Unmarshal(rec.Body.Bytes(), &appended); err != nil {
		t.Fatalf("decode append: %v", err)
	}
	if len(appended.Inserted) != 1 {
		t.Fatalf("append: inserted %v, want exactly the fragment block", appended.Inserted)
	}

	// Blank-only content stays a no-op on the append path.
	rec = doJSON(t, e, http.MethodPost, md+"/append", `{"content":"\n\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("append blank: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &appended); err != nil {
		t.Fatalf("decode append blank: %v", err)
	}
	if len(appended.Inserted) != 0 {
		t.Fatalf("append blank: inserted %v, want none", appended.Inserted)
	}
}

// markdownSet PUTs whole-document markdown and decodes the response.
func markdownSet(t *testing.T, e http.Handler, path, content string) (out mdEditResp) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		t.Fatalf("encode content: %v", err)
	}
	rec := doJSON(t, e, http.MethodPut, path, string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT markdown %q: %d %s", content, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode set response: %v", err)
	}
	return out
}
