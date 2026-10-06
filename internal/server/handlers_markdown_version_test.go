package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestServer_MarkdownVersion pins the compare-and-swap contract of
// PUT .../editor/markdown: GET and PUT hand out a version, a PUT
// carrying the current one writes and returns the next, and a stale or
// unknown one is a 409 that writes nothing and carries the current body.
func TestServer_MarkdownVersion(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	md := "/v1/spaces/" + spaceId + "/objects/" + objectId + "/editor/editor_blocks/markdown"

	first := markdownSet(t, e, md, "alpha\n\nbeta")
	if first.Version == "" {
		t.Fatalf("unconditional PUT: no version in %+v", first)
	}
	doc := getMarkdownDoc(t, e, md)
	if doc.Content != "alpha\n\nbeta" || doc.Version != first.Version {
		t.Fatalf("GET after PUT: %+v, want the PUT's version %q", doc, first.Version)
	}
	if again := getMarkdownDoc(t, e, md); again.Version != doc.Version {
		t.Fatalf("a read moved the version: %q → %q", doc.Version, again.Version)
	}

	// Saves chain: each 200's version is the next ifVersion.
	second := markdownPut(t, e, md, "alpha\n\nbeta\n\ngamma", doc.Version)
	if second.Version == "" || second.Version == doc.Version {
		t.Fatalf("second save: version %q, want a new one after %q", second.Version, doc.Version)
	}
	third := markdownPut(t, e, md, "alpha\n\nbeta\n\ngamma\n\ndelta", second.Version)
	if third.Version == second.Version {
		t.Fatalf("third save: version did not move from %q", second.Version)
	}
	current := getMarkdownDoc(t, e, md)
	if current.Version != third.Version {
		t.Fatalf("GET version %q, want the last save's %q", current.Version, third.Version)
	}

	// A save that writes nothing keeps the version.
	same := markdownPut(t, e, md, current.Content, current.Version)
	if len(same.Inserted)+len(same.Updated)+len(same.Deleted) != 0 || same.Version != current.Version {
		t.Fatalf("no-op save: %+v, want no writes and version %q", same, current.Version)
	}

	for _, stale := range []string{doc.Version, "unknown"} {
		got := markdownPutConflict(t, e, md, "rewritten", stale)
		if got.Content != current.Content || got.Version != current.Version {
			t.Errorf("ifVersion %q: conflict details %+v, want the current body %+v", stale, got, current)
		}
	}
	if after := getMarkdownDoc(t, e, md); after != current {
		t.Fatalf("a rejected save wrote: %+v, want %+v", after, current)
	}
}

// TestServer_MarkdownVersionConcurrentSaves pins that conditional saves
// carrying one ifVersion do not both write: each save's write is
// conditional on the read it was computed from, so once one lands the
// others are refused and answer 409.
func TestServer_MarkdownVersionConcurrentSaves(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	md := "/v1/spaces/" + spaceId + "/objects/" + objectId + "/editor/editor_blocks/markdown"
	markdownSet(t, e, md, "alpha\n\nbeta")
	base := getMarkdownDoc(t, e, md)

	const savers = 4
	codes := make(chan int, savers)
	for i := range savers {
		go func() {
			body, _ := json.Marshal(api.MarkdownSetRequest{
				Content:   base.Content + "\n\nsaver " + strconv.Itoa(i),
				IfVersion: base.Version,
			})
			codes <- doJSON(t, e, http.MethodPut, md, string(body)).Code
		}()
	}
	var ok, conflict int
	for range savers {
		switch code := <-codes; code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Errorf("concurrent save: status %d", code)
		}
	}
	if ok != 1 || conflict != savers-1 {
		t.Fatalf("concurrent saves: %d written, %d conflicts; want 1 and %d", ok, conflict, savers-1)
	}
	if got := len(blocksList(t, e, "/v1/spaces/"+spaceId+"/objects/"+objectId).Records); got != 3 {
		t.Fatalf("%d blocks after the saves, want 3 (one saver's paragraph)", got)
	}
}

// TestServer_MarkdownVersionMovesOnEveryBlockWrite pins that the
// version moves on writes the rendered body does not show — a style
// key, a nested block and its delete — and on a delete, so a save from
// before a delete is a 409 and cannot bring the block back.
func TestServer_MarkdownVersionMovesOnEveryBlockWrite(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, objectId := setupBlocksFixture(t, e)
	base := "/v1/spaces/" + spaceId + "/objects/" + objectId
	md := base + "/editor/editor_blocks/markdown"

	markdownSet(t, e, md, "alpha\n\nbeta")
	blocks := blocksList(t, e, base).Records
	if len(blocks) != 2 {
		t.Fatalf("seed: %d blocks, want 2", len(blocks))
	}
	prev := getMarkdownDoc(t, e, md)
	moved := func(step, wantContent string) {
		t.Helper()
		got := getMarkdownDoc(t, e, md)
		if got.Content != wantContent {
			t.Fatalf("%s: content %q, want %q", step, got.Content, wantContent)
		}
		if got.Version == prev.Version {
			t.Fatalf("%s: version stayed %q", step, got.Version)
		}
		prev = got
	}

	rec := doJSON(t, e, http.MethodPatch, base+"/editor/editor_blocks/blocks/"+blocks[0].Id, `{"set":{"style.color":"red"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch style: %d %s", rec.Code, rec.Body.String())
	}
	moved("style key the markdown does not render", "alpha\n\nbeta")

	child := blocksCreate(t, e, base, `{"type":"paragraph","text":"child","nav":{"parentId":"`+blocks[0].Id+`"}}`)
	moved("nested block", "alpha\n\nbeta")

	// A later write to another block leaves the child below the highest
	// stamp. Deleting it changes no body and no live stamp, so only the
	// child's tombstone can move the version: deleted records count.
	rec = doJSON(t, e, http.MethodPatch, base+"/editor/editor_blocks/blocks/"+blocks[0].Id, `{"set":{"style.color":"blue"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch style again: %d %s", rec.Code, rec.Body.String())
	}
	moved("another style write", "alpha\n\nbeta")
	rec = doJSON(t, e, http.MethodDelete, base+"/editor/editor_blocks/blocks/"+child.Id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete nested block: %d %s", rec.Code, rec.Body.String())
	}
	moved("nested block delete", "alpha\n\nbeta")

	beforeDelete := prev
	rec = doJSON(t, e, http.MethodDelete, base+"/editor/editor_blocks/blocks/"+blocks[1].Id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete block: %d %s", rec.Code, rec.Body.String())
	}
	moved("block delete", "alpha")

	got := markdownPutConflict(t, e, md, beforeDelete.Content, beforeDelete.Version)
	if got.Content != "alpha" {
		t.Errorf("conflict details content %q, want %q", got.Content, "alpha")
	}
	if after := getMarkdownDoc(t, e, md); after.Content != "alpha" {
		t.Fatalf("a save from before the delete brought the block back: %q", after.Content)
	}
}

// getMarkdownDoc GETs .../editor/markdown with its version.
func getMarkdownDoc(t *testing.T, e http.Handler, path string) api.MarkdownDocument {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET markdown: %d %s", rec.Code, rec.Body.String())
	}
	var doc api.MarkdownDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode markdown: %v", err)
	}
	if doc.Version == "" {
		t.Fatalf("GET markdown: no version in %s", rec.Body.String())
	}
	return doc
}

// markdownPut PUTs content conditioned on ifVersion and expects a 200.
func markdownPut(t *testing.T, e http.Handler, path, content, ifVersion string) (out mdEditResp) {
	t.Helper()
	body, _ := json.Marshal(api.MarkdownSetRequest{Content: content, IfVersion: ifVersion})
	rec := doJSON(t, e, http.MethodPut, path, string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT markdown ifVersion=%q: %d %s", ifVersion, rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode set response: %v", err)
	}
	return out
}

// markdownPutConflict PUTs content conditioned on ifVersion, expects
// 409 markdown.conflict, and returns the current body from details.
func markdownPutConflict(t *testing.T, e http.Handler, path, content, ifVersion string) api.MarkdownDocument {
	t.Helper()
	body, _ := json.Marshal(api.MarkdownSetRequest{Content: content, IfVersion: ifVersion})
	rec := doJSON(t, e, http.MethodPut, path, string(body))
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT markdown ifVersion=%q: %d %s, want 409", ifVersion, rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code    string               `json:"code"`
			Details api.MarkdownDocument `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if env.Error.Code != api.ErrMarkdownConflict {
		t.Fatalf("conflict code %q, want %q", env.Error.Code, api.ErrMarkdownConflict)
	}
	return env.Error.Details
}
