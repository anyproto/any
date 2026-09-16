//go:build fts && vector && !gomobile

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/indexer"
)

// linkKeys renders edges as "source→target(kind)" for assertions.
func linkKeys(links []api.Link) map[string]string {
	out := map[string]string{}
	for _, l := range links {
		out[l.Source.Dataset+"/"+l.Source.RecordId+"→"+l.Target.Uri] = l.Kind
	}
	return out
}

func getBacklinks(t *testing.T, e http.Handler, spaceId, objectId, query string) api.BacklinksResponse {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/objects/"+objectId+"/backlinks"+query, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("backlinks: %d %s", rec.Code, rec.Body.String())
	}
	var out api.BacklinksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func getLinks(t *testing.T, e http.Handler, spaceId, objectId, query string) api.LinksResponse {
	t.Helper()
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/objects/"+objectId+"/links"+query, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("links: %d %s", rec.Code, rec.Body.String())
	}
	var out api.LinksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestIndexer_Links drives the link index end to end over HTTP: edges
// from editor blocks (inline link, whole-line card, block reference,
// mention), a chat message (text link + attachment), and a relation
// property value; the reads (object vs parts, narrowing, kind filter,
// forward links, account-wide); removal on block delete and on object
// delete; and the bus signal.
func TestIndexer_Links(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	var mu sync.Mutex
	var signalled []string
	st, err := indexer.OpenStoreInMemory(ctx, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	ix := indexer.New(d.sdk, d.eng.chunkers, st, indexer.Options{
		Debounce: 10 * time.Millisecond,
		OnLinks: func(spaceId string, targets []string) {
			mu.Lock()
			signalled = append(signalled, targets...)
			mu.Unlock()
		},
	})
	d.indexer = ix
	defer func() { _ = ix.Close() }()

	spaceId := mustCreateSpace(t, e, "Links")
	target := mustCreateObject(t, e, spaceId, `{"type":"page","initialProperties":{"any":{"name":"target"}}}`)
	other := mustCreateObject(t, e, spaceId, `{"type":"page","initialProperties":{"any":{"name":"other"}}}`)
	targetUri := "any://o/" + spaceId + "/" + target

	// Editor: four blocks on one page.
	page := mustCreateModuleObject(t, e, spaceId, "editor")
	blocks := "/v1/spaces/" + spaceId + "/objects/" + page + "/editor/editor_blocks/blocks"
	inline := mustModify(t, e, http.MethodPost, blocks,
		`{"type":"paragraph","text":"see [target](any://`+target+`) inline"}`, http.StatusCreated).RecordIds[0]
	card := mustModify(t, e, http.MethodPost, blocks,
		`{"type":"paragraph","text":"[Target](`+targetUri+`)"}`, http.StatusCreated).RecordIds[0]
	blockRef := mustModify(t, e, http.MethodPost, blocks,
		`{"type":"paragraph","text":"cites [a block](`+targetUri+`/editor_blocks/blk9) and [other](any://`+other+`)"}`, http.StatusCreated).RecordIds[0]
	mustModify(t, e, http.MethodPost, blocks,
		`{"type":"paragraph","text":"hey [Z](any://m/`+spaceId+`/identity1)"}`, http.StatusCreated)

	// Chat: one message with a text link and an attachment.
	chat := mustCreateModuleObject(t, e, spaceId, "chat")
	msg := mustModify(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects/"+chat+"/chat/messages",
		`{"text":"moved to [target](`+targetUri+`)","attachments":{"a1":{"type":"link","link":"any://`+other+`"}}}`, http.StatusCreated).RecordIds[0]

	// A relation property on a user type, pointing at the target.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types", `{"name":"Doc","xKey":"doc"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create type: %d %s", rec.Code, rec.Body.String())
	}
	var tr api.TypesCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &tr)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/"+tr.TypeId+"/properties",
		`{"name":"Related","xKey":"related","kind":"array","xFormat":{"type":"relation","config":{"multiple":true}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create prop: %d %s", rec.Code, rec.Body.String())
	}
	var pr api.AddPropertyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pr)
	doc := mustCreateObject(t, e, spaceId, `{"type":"`+tr.TypeId+`","initialProperties":{"`+
		tr.TypeId+`":{"`+pr.PropId+`":["any://`+target+`","any://`+other+`"]}}}`)

	sdkSpace, err := d.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}

	// --- backlinks of the target ---------------------------------------
	bl := getBacklinks(t, e, spaceId, target, "")
	wantObject := map[string]string{
		"editor_blocks/" + inline + "→" + targetUri: api.LinkKindLink,
		"editor_blocks/" + card + "→" + targetUri:   api.LinkKindCard,
		"chat_messages/" + msg + "→" + targetUri:    api.LinkKindLink,
		"prop/" + pr.PropId + "→" + targetUri:       api.LinkKindRelation,
	}
	if got := linkKeys(bl.Object); len(got) != len(wantObject) {
		t.Fatalf("object backlinks = %v, want %v", got, wantObject)
	} else {
		for k, kind := range wantObject {
			if got[k] != kind {
				t.Errorf("%s: kind %q, want %q", k, got[k], kind)
			}
		}
	}
	if got := linkKeys(bl.Parts); len(got) != 1 || got["editor_blocks/"+blockRef+"→"+targetUri+"/editor_blocks/blk9"] != api.LinkKindLink {
		t.Fatalf("part backlinks = %v, want the block reference", got)
	}
	for _, l := range bl.Object {
		if l.Source.SpaceId != spaceId || l.Target.ObjectId != target || l.Target.Kind != "o" {
			t.Errorf("edge fields wrong: %+v", l)
		}
		if l.Source.Dataset == "prop" && l.Source.ObjectId != doc {
			t.Errorf("relation edge source = %s, want %s", l.Source.ObjectId, doc)
		}
	}

	// Kind filter and narrowing to one block.
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "?kind=card&kind=relation").Object); len(got) != 2 {
		t.Errorf("kind-filtered backlinks = %v, want card + relation", got)
	}
	narrowed := getBacklinks(t, e, spaceId, target, "?record=blk9&dataset=editor_blocks")
	if len(narrowed.Object) != 1 || len(narrowed.Parts) != 0 || narrowed.Object[0].Source.RecordId != blockRef {
		t.Errorf("narrowed backlinks = %+v", narrowed)
	}
	if rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/objects/"+target+"/backlinks?record=x", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("record without dataset: %d", rec.Code)
	}
	if rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/objects/"+target+"/backlinks?kind=Not%20A%20Slug", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad kind: %d", rec.Code)
	}

	// The other object: the block reference's sibling link, the chat
	// attachment and the relation value.
	otherUri := "any://o/" + spaceId + "/" + other
	if got := linkKeys(getBacklinks(t, e, spaceId, other, "").Object); len(got) != 3 ||
		got["chat_messages/"+msg+"→"+otherUri] != api.LinkKindLink || got["prop/"+pr.PropId+"→"+otherUri] != api.LinkKindRelation {
		t.Errorf("backlinks of other = %v", got)
	}

	// Forward links of the page, and of one block.
	fwd := linkKeys(getLinks(t, e, spaceId, page, "").Links)
	if len(fwd) != 5 { // inline, card, blockRef×2, mention
		t.Errorf("forward links of the page = %v, want 5", fwd)
	}
	if fwd["editor_blocks/"+blockRef+"→any://m/"+spaceId+"/identity1"] != "" {
		t.Errorf("mention attributed to the wrong block: %v", fwd)
	}
	one := getLinks(t, e, spaceId, page, "?record="+card+"&dataset=editor_blocks")
	if len(one.Links) != 1 || one.Links[0].Kind != api.LinkKindCard {
		t.Errorf("links of the card block = %+v", one)
	}
	if got := getLinks(t, e, spaceId, doc, "?prop="+pr.PropId); len(got.Links) != 2 {
		t.Errorf("links of the relation value = %+v", got)
	}

	// Account-wide.
	rec = doJSON(t, e, http.MethodGet, "/v1/backlinks?target="+targetUri, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("backlinks all: %d %s", rec.Code, rec.Body.String())
	}
	var all api.BacklinksAllResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &all)
	if len(all.Spaces) != 1 || all.Spaces[0].SpaceId != spaceId || len(all.Spaces[0].Object) != 4 || len(all.Spaces[0].Parts) != 1 {
		t.Errorf("account-wide backlinks = %+v", all)
	}
	if rec := doJSON(t, e, http.MethodGet, "/v1/backlinks?target=any://"+target, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("in-space target on the account-wide read: %d", rec.Code)
	}

	// The bus signal named the target.
	mu.Lock()
	seen := false
	for _, s := range signalled {
		if s == targetUri {
			seen = true
		}
	}
	mu.Unlock()
	if !seen {
		t.Errorf("OnLinks never named the target: %v", signalled)
	}

	// Cap: the read is cut before the split and says so.
	if capped := getBacklinks(t, e, spaceId, target, "?limit=2"); !capped.Truncated || len(capped.Object)+len(capped.Parts) != 2 {
		t.Errorf("capped backlinks = %+v", capped)
	}
	if capped := getBacklinks(t, e, spaceId, target, "?limit=2000000000"); capped.Truncated {
		t.Errorf("an oversized limit clamps to the cap, %+v", capped)
	}
	// Forward read of one collection.
	if got := getLinks(t, e, spaceId, page, "?dataset=editor_blocks"); len(got.Links) != 5 {
		t.Errorf("links of the page's editor collection = %+v", got)
	}
	if rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+spaceId+"/objects/"+target+"/backlinks?dataset=editor_blocks", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("dataset without record on backlinks: %d", rec.Code)
	}
	// The relation edge names its type.
	for _, l := range bl.Object {
		if l.Source.Dataset == "prop" && l.Source.TypeId != tr.TypeId {
			t.Errorf("relation edge typeId = %q, want %s", l.Source.TypeId, tr.TypeId)
		}
	}

	// The first advance stamped the layout: a restart must not backfill.
	if v, err := st.LinksVersion(ctx, spaceId); err != nil || v == 0 {
		t.Errorf("layout stamp after the first advance = %d (%v)", v, err)
	}
	if need, _ := st.LinksBackfillNeeded(ctx, spaceId); need {
		t.Error("a space indexed in this process needs a backfill")
	}

	// A property defined a moment ago indexes the value written right
	// after it: the type change refreshes the catalog snapshot inside
	// the page, no TTL wait.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/types/"+tr.TypeId+"/properties",
		`{"name":"Also","xKey":"also","kind":"array","xFormat":{"type":"relation","config":{"multiple":true}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create prop: %d %s", rec.Code, rec.Body.String())
	}
	var pr2 api.AddPropertyResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &pr2)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/properties/"+doc+"/set/"+tr.TypeId,
		`{"patch":{"`+pr2.PropId+`":["any://`+target+`"]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set new relation: %d %s", rec.Code, rec.Body.String())
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); got["prop/"+pr2.PropId+"→"+targetUri] != api.LinkKindRelation {
		t.Errorf("value of a just-defined property not indexed: %v", got)
	}
	// Removing the definition drops its edges on the row's next change.
	if rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+spaceId+"/types/"+tr.TypeId+"/properties/"+pr2.PropId, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("remove prop: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/properties/"+doc+"/set/any", `{"patch":{"name":"doc"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("touch doc: %d %s", rec.Code, rec.Body.String())
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); got["prop/"+pr2.PropId+"→"+targetUri] != "" || got["prop/"+pr.PropId+"→"+targetUri] != api.LinkKindRelation {
		t.Errorf("after removing the definition: %v", got)
	}
	// The off-switch: a relation marked none reports nothing.
	rec = doJSON(t, e, http.MethodPatch, "/v1/spaces/"+spaceId+"/types/"+tr.TypeId+"/properties/"+pr.PropId, `{"set":{"xFormat.links":"none"}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("mark none: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/properties/"+doc+"/set/any", `{"patch":{"name":"doc2"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("touch doc: %d %s", rec.Code, rec.Body.String())
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); got["prop/"+pr.PropId+"→"+targetUri] != "" {
		t.Errorf("a none-marked relation still reports: %v", got)
	}
	if rec := doJSON(t, e, http.MethodPatch, "/v1/spaces/"+spaceId+"/types/"+tr.TypeId+"/properties/"+pr.PropId, `{"unset":["xFormat.links"]}`); rec.Code != http.StatusNoContent {
		t.Fatalf("unmark: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/properties/"+doc+"/set/any", `{"patch":{"name":"doc3"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("touch doc: %d %s", rec.Code, rec.Body.String())
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); got["prop/"+pr.PropId+"→"+targetUri] != api.LinkKindRelation {
		t.Errorf("after unmarking: %v", got)
	}

	// --- backfill: a db indexed before the sink existed ---------------
	if err := st.ResetLinks(ctx, spaceId); err != nil {
		t.Fatal(err)
	}
	if err := st.UnstampLinks(ctx, spaceId); err != nil {
		t.Fatal(err)
	}
	if got := getBacklinks(t, e, spaceId, target, ""); len(got.Object) != 0 {
		t.Fatalf("edges survived the reset: %+v", got)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); len(got) != len(wantObject) {
		t.Errorf("after backfill: %v, want %v", got, wantObject)
	}
	if need, _ := st.LinksBackfillNeeded(ctx, spaceId); need {
		t.Error("space still needs a backfill after one ran")
	}

	// --- retype ----------------------------------------------------------
	// Retyping the page away from the type that declares the editor
	// collection evicts its editor edges (a structural prefix, no
	// re-extraction); retyping the doc away from the relation's type
	// drops the value's edges (the value is stale, not a live
	// reference). The target type declares nothing — `page` would keep
	// the editor collection, which is shared.
	edType := installModuleType(t, e, spaceId, "editor")
	plain := plainType(t, e, spaceId)
	if _, err := sdkSpace.Properties().SetType(ctx, page, plain); err != nil {
		t.Fatal(err)
	}
	if _, err := sdkSpace.Properties().SetType(ctx, doc, plain); err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := getLinks(t, e, spaceId, page, ""); len(got.Links) != 0 {
		t.Errorf("edges of the evicted editor collection survived: %+v", got)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); len(got) != 1 || got["chat_messages/"+msg+"→"+targetUri] == "" {
		t.Errorf("after the retypes: %v, want only the chat edge", got)
	}
	// Set the declaring types back: the value comes back on the row's
	// next change (the retype itself), the blocks on theirs.
	if _, err := sdkSpace.Properties().SetType(ctx, page, edType); err != nil {
		t.Fatal(err)
	}
	if _, err := sdkSpace.Properties().SetType(ctx, doc, tr.TypeId); err != nil {
		t.Fatal(err)
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); len(got) != len(wantObject) {
		t.Errorf("after the types come back: %v, want %v", got, wantObject)
	}

	// --- removals ------------------------------------------------------
	mustModify(t, e, http.MethodDelete, blocks+"/"+card, "", http.StatusOK)
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); len(got) != 3 || got["editor_blocks/"+card+"→"+targetUri] != "" {
		t.Errorf("after block delete: %v", got)
	}
	// Editing a block re-extracts it: the inline link moves to `other`.
	mustModify(t, e, http.MethodPatch, blocks+"/"+inline, `{"set":{"text":"now [other](any://`+other+`)"}}`, http.StatusOK)
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); got["editor_blocks/"+inline+"→"+targetUri] != "" {
		t.Errorf("after block edit the old edge survived: %v", got)
	}
	// Deleting the source object evicts every edge it held.
	if rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+spaceId+"/objects/"+page, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete page: %d %s", rec.Code, rec.Body.String())
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := getLinks(t, e, spaceId, page, ""); len(got.Links) != 0 {
		t.Errorf("edges of the deleted page survived: %+v", got)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); len(got) != 2 {
		t.Errorf("after page delete: %v, want chat + relation", got)
	}
	// Clearing the relation value drops its edges.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/properties/"+doc+"/set/"+tr.TypeId,
		`{"patch":{"`+pr.PropId+`":[]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear relation: %d %s", rec.Code, rec.Body.String())
	}
	if err := ix.SyncSpace(ctx, sdkSpace); err != nil {
		t.Fatal(err)
	}
	if got := linkKeys(getBacklinks(t, e, spaceId, target, "").Object); len(got) != 1 || got["chat_messages/"+msg+"→"+targetUri] == "" {
		t.Errorf("after relation clear: %v, want only the chat edge", got)
	}
}

// TestLinks_BusBridge: the engine turns the indexer's page signal into
// one device-scope links.updated event, capped.
func TestLinks_BusBridge(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	id, ch := d.eventsHub().subscribe(eventFilter{types: []string{api.EventLinksUpdated}})
	defer d.eventsHub().unsubscribe(id)

	targets := make([]string, api.MaxEventLinksTargets+5)
	for i := range targets {
		targets[i] = "any://o/sp/obj" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	d.indexerLinksFor(d.eng)("sp", targets)
	select {
	case ev := <-ch:
		if ev.Scope != api.EventScopeDevice || ev.Sender == nil || !ev.Sender.Self {
			t.Errorf("envelope = %+v", ev)
		}
		var data api.EventLinksUpdatedData
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatal(err)
		}
		if data.SpaceId != "sp" || len(data.Targets) != api.MaxEventLinksTargets || !data.Truncated {
			t.Errorf("payload = %d targets truncated=%v space=%s", len(data.Targets), data.Truncated, data.SpaceId)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no links.updated event on the hub")
	}
}

// TestLinks_IndexDisabled: without an indexer the reads answer 409
// index.disabled, like /search.
func TestLinks_IndexDisabled(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	d.indexer = nil
	spaceId := mustCreateSpace(t, e, "LinksOff")
	obj := mustCreateObject(t, e, spaceId, `{"type":"page"}`)
	for _, path := range []string{
		"/v1/spaces/" + spaceId + "/objects/" + obj + "/backlinks",
		"/v1/spaces/" + spaceId + "/objects/" + obj + "/links",
		"/v1/backlinks?target=any://o/" + spaceId + "/" + obj,
	} {
		rec := doJSON(t, e, http.MethodGet, path, "")
		if rec.Code != http.StatusConflict || errCode(t, rec.Body.Bytes()) != "index.disabled" {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
