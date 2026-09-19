package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/catalog"
	"github.com/anyproto/any/internal/page"
)

// The usecase catalog over HTTP: the embedded entries set up end to
// end, the dependency walk, and the rules that only bite on the install
// path. A test that needs a catalog the embedded one does not carry
// compiles its own and sets it on its deps.

// catalogForTest compiles a yaml catalog for one test's deps.
func catalogForTest(t testing.TB, yaml string) *compiledCatalog {
	t.Helper()
	cc, problems := compileCatalog([]byte(yaml))
	if len(problems) > 0 {
		t.Fatalf("test catalog: %v", problems)
	}
	return cc
}

// setupUsecase POSTs a setup and decodes the reply.
func setupUsecase(t *testing.T, e http.Handler, usecase, spaceId string) api.CatalogSetupResponse {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/catalog/"+usecase+"/setup", `{"spaceId":"`+spaceId+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup %s: %d %s", usecase, rec.Code, rec.Body.String())
	}
	var out api.CatalogSetupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode setup: %v", err)
	}
	return out
}

func bundleIds(res api.CatalogSetupResponse) []string {
	out := make([]string, 0, len(res.Bundles))
	for _, b := range res.Bundles {
		out = append(out, b.Id)
	}
	return out
}

// objectRow reads an object's row.
func objectRow(t *testing.T, e http.Handler, spaceId, objectId string) map[string]any {
	t.Helper()
	var res api.ObjectGetResponse
	decodeGet(t, e, "/v1/spaces/"+spaceId+"/objects/"+objectId, &res)
	var row map[string]any
	if err := json.Unmarshal(res.Record, &row); err != nil {
		t.Fatalf("decode record: %v", err)
	}
	return row
}

// rowType is the object's one type (`any.type`); rowCollections what it
// is filed under (`any.collections`).
func rowType(row map[string]any) string {
	anyNs, _ := row["any"].(map[string]any)
	t, _ := anyNs["type"].(string)
	return t
}

func rowCollections(row map[string]any) []string {
	anyNs, _ := row["any"].(map[string]any)
	list, _ := anyNs["collections"].([]any)
	out := make([]string, 0, len(list))
	for _, v := range list {
		out = append(out, v.(string))
	}
	return out
}

func TestServer_CatalogListAndGet(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	var list api.CatalogListResponse
	decodeGet(t, e, "/v1/catalog", &list)
	ids := map[string]api.CatalogUsecase{}
	for _, u := range list.Usecases {
		ids[u.Id] = u
	}
	for _, want := range []string{"wiki", "collections", "journal", "meetings", "general-chat", "people", "contact", "contacts", "crm"} {
		if _, ok := ids[want]; !ok {
			t.Fatalf("usecase %s missing from the list", want)
		}
	}
	if got := ids["crm"].Requires; !slices.Equal(got, []string{"contacts"}) {
		t.Fatalf("crm requires = %v", got)
	}

	var wiki api.CatalogUsecase
	decodeGet(t, e, "/v1/catalog/wiki", &wiki)
	// The wiki files pages it does not type: it declares a COLLECTION.
	if len(wiki.Bundles) != 1 || wiki.Bundles[0].Id != "system:wiki/v1" || wiki.Bundles[0].Type != nil ||
		wiki.Bundles[0].Collection == nil || wiki.Bundles[0].Collection.XKey != "wiki" || !wiki.Bundles[0].Hidden {
		t.Fatalf("wiki entry: %+v", wiki)
	}
	if got := wiki.Bundles[0].Miniapp["bundle"]; got != "system:wiki/v1" {
		t.Fatalf("wiki miniapp.bundle = %v", got)
	}
	// A bundle declared as `miniapp: {}` lists what setup writes: the
	// bundle id filled in, never an absent key.
	if got := ids["collections"].Bundles[0].Miniapp["bundle"]; got != "system:collections/v1" {
		t.Fatalf("collections miniapp on the wire = %v", ids["collections"].Bundles[0].Miniapp)
	}

	rec := doJSON(t, e, http.MethodGet, "/v1/catalog/nope", "")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), api.ErrCatalogNotFound) {
		t.Fatalf("unknown usecase: %d %s", rec.Code, rec.Body.String())
	}
}

// The sidebar contract every app root shares: one root per bundle, the
// shared `pos` / `hidden` written as ordinary property values, and a
// re-setup that adopts without resetting either.
func TestServer_CatalogSetupSidebarState(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogSidebar")

	for id, bundleId := range map[string]string{"journal": "system:journal/v2", "meetings": "system:meetings/v1"} {
		t.Run(id, func(t *testing.T) {
			res := setupUsecase(t, e, id, sp.Id)
			if res.Usecase != id {
				t.Fatalf("setup reply: %+v", res)
			}
			var b api.CatalogSetupBundle
			for _, entry := range res.Bundles {
				if entry.Id == bundleId {
					b = entry
				}
			}
			if !b.Installed || b.Bundle.Derived || b.Miniapp["bundle"] != bundleId {
				t.Fatalf("app bundle: %+v", b)
			}
			row := objectRow(t, e, sp.Id, b.Bundle.RootId)
			ma, _ := row["miniapp"].(map[string]any)
			if ma["bundle"] != bundleId || ma["pos"] != nil || ma["hidden"] != nil {
				t.Fatalf("initial sidebar state: %v", ma)
			}

			// Ordinary property writes carry both ordering and hiding; setup
			// must neither reset the values nor mint another sidebar entry.
			rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/properties/"+b.Bundle.RootId+"/set/miniapp",
				`{"patch":{"pos":"a1","hidden":true}}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("set sidebar state: %d %s", rec.Code, rec.Body.String())
			}
			for range 2 {
				for _, entry := range setupUsecase(t, e, id, sp.Id).Bundles {
					if entry.Installed {
						t.Fatalf("repeated setup re-installed %s: %+v", entry.Id, entry)
					}
					if entry.Id == bundleId && entry.Bundle.RootId != b.Bundle.RootId {
						t.Fatalf("repeated setup moved the app root: %+v", entry)
					}
				}
			}
			row = objectRow(t, e, sp.Id, b.Bundle.RootId)
			ma, _ = row["miniapp"].(map[string]any)
			if ma["pos"] != "a1" || ma["hidden"] != true || ma["bundle"] != bundleId {
				t.Fatalf("setup changed shared sidebar state: %v", ma)
			}
			rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
				`{"filter":{"miniapp.bundle":"`+bundleId+`"}}`)
			if rec.Code != http.StatusOK {
				t.Fatalf("query roots: %d %s", rec.Code, rec.Body.String())
			}
			var q api.QueryResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil || len(q.Records) != 1 {
				t.Fatalf("expected one app root: %s (%v)", rec.Body.String(), err)
			}
		})
	}
}

// Journal brings a collection: one root that is the sidebar entry AND the
// hidden `journal` collection. An entry is a `page` filed under it — the
// collection carries the day, the page carries the body.
func TestServer_CatalogSetupJournal(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogJournal")

	res := setupUsecase(t, e, "journal", sp.Id)
	if got := bundleIds(res); !slices.Equal(got, []string{"system:journal/v2"}) {
		t.Fatalf("a new space receives the collection only: %v", got)
	}
	b := res.Bundles[0]
	if b.TypeId != "" || b.CollectionId != b.Bundle.RootId || b.Properties["date"] == "" {
		t.Fatalf("journal bundle: %+v", b)
	}
	// The root is the collection DEFINITION plus the sidebar entry: the
	// marker in any.type, `miniapp` among its collections. It never
	// matches a member query, or the app itself would read as an entry.
	row := objectRow(t, e, sp.Id, b.CollectionId)
	if got := rowType(row); got != "__collection__" {
		t.Fatalf("journal root any.type = %q", got)
	}
	if cols := rowCollections(row); !slices.Contains(cols, "miniapp") || slices.Contains(cols, b.CollectionId) {
		t.Fatalf("journal root collections = %v", cols)
	}
	var info api.CollectionInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/collections/"+b.CollectionId, &info)
	if info.XKey != "journal" || !info.Hidden {
		t.Fatalf("journal collection info: %+v", info)
	}

	// An entry is one object: a page, so its body is the shared editor
	// collection, filed under the journal, which carries the day.
	day := `{"$date":"2026-09-12T00:00:00.000Z"}`
	entry := mustCreateObject(t, e, sp.Id, `{"type":"page","collections":["`+b.CollectionId+`"],"initialProperties":{"`+
		b.CollectionId+`":{"`+b.Properties["date"]+`":`+day+`}}}`)
	rec := doJSON(t, e, http.MethodPost,
		"/v1/spaces/"+sp.Id+"/objects/"+entry+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"woke up"}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("write the entry's body: %d %s", rec.Code, rec.Body.String())
	}
	// The day is the query key the Journal surface reads by.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
		`{"filter":{"any.collections":"`+b.CollectionId+`","`+b.CollectionId+`.`+b.Properties["date"]+`":`+day+`}}`)
	var q api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil || len(q.Records) != 1 ||
		!strings.Contains(string(q.Records[0]), entry) {
		t.Fatalf("query the day: %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
}

// A meeting is one OBJECT: the type says what it is, and its three
// surfaces are the notes (the common editor), a second editor for the
// summary and a transcript of one record per spoken turn.
func TestServer_CatalogSetupMeetings(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogMeetings")

	res := setupUsecase(t, e, "meetings", sp.Id)
	byId := map[string]api.CatalogSetupBundle{}
	for _, b := range res.Bundles {
		byId[b.Id] = b
	}
	meeting, app := byId["system:meeting/v1"], byId["system:meetings/v1"]
	if meeting.TypeId != meeting.Bundle.RootId || app.Miniapp["bundle"] != "system:meetings/v1" {
		t.Fatalf("meetings usecase: %+v", res.Bundles)
	}
	// The type is a content type users see, not a hidden marker.
	var info api.TypeInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+meeting.TypeId, &info)
	if info.XKey != "meeting" || info.Hidden {
		t.Fatalf("meeting type info: %+v", info)
	}
	for _, xk := range []string{"date", "duration", "participants", "labels", "words", "source"} {
		if meeting.Properties[xk] == "" {
			t.Fatalf("property %s unresolved: %+v", xk, meeting.Properties)
		}
	}

	var parts api.TypePartsListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+meeting.TypeId+"/parts", &parts)
	collections := map[string]string{}
	for _, p := range parts.Parts {
		if len(p.Datasets) != 1 {
			t.Fatalf("part %s datasets: %+v", p.Key, p.Datasets)
		}
		collections[p.Key] = p.Datasets[0].Collection
	}
	// The notes share the editor's canonical collection (so a meeting and a
	// page have one body); the summary is a SECOND editor of its own.
	if collections["notes"] != "editor_blocks" ||
		collections["summary"] != meeting.TypeId+"_summary" ||
		collections["transcript"] != meeting.TypeId+"_transcript" {
		t.Fatalf("meeting surfaces: %v", collections)
	}

	obj := mustCreateObject(t, e, sp.Id, `{"type":"`+meeting.TypeId+`","initialProperties":{"any":{"name":"Weekly sync"},"`+
		meeting.TypeId+`":{"`+meeting.Properties["date"]+`":{"$date":"2026-09-11T09:00:00.000Z"},"`+
		meeting.Properties["participants"]+`":["Ann","Bo"]}}}`)
	for _, coll := range []string{"editor_blocks", collections["summary"]} {
		rec := doJSON(t, e, http.MethodPut, "/v1/spaces/"+sp.Id+"/objects/"+obj+"/editor/"+coll+"/markdown",
			`{"content":"# `+coll+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("write %s: %d %s", coll, rec.Code, rec.Body.String())
		}
	}

	// The transcript is upserted by the provider's segment id, so a
	// re-ingest of the same turn is one record, and it reads back in order.
	turns := `{"objectId":"` + obj + `","dataset":"` + collections["transcript"] + `","records":[` +
		`{"id":"seg-2","fields":{"startedAt":{"$date":"2026-09-11T09:00:20.000Z"},"speaker":"Bo","text":"morning"}},` +
		`{"id":"seg-1","fields":{"startedAt":{"$date":"2026-09-11T09:00:05.000Z"},"speaker":"Ann","text":"hello"}}]}`
	for range 2 {
		if rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/upsert", turns); rec.Code != http.StatusOK {
			t.Fatalf("ingest the transcript: %d %s", rec.Code, rec.Body.String())
		}
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/query",
		`{"objectId":"`+obj+`","dataset":"`+collections["transcript"]+`","sort":["startedAt"]}`)
	var q api.QueryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil || len(q.Records) != 2 {
		t.Fatalf("read the transcript: %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	var turn struct {
		Id      string `json:"id"`
		Speaker string `json:"speaker"`
	}
	if err := json.Unmarshal(q.Records[0], &turn); err != nil {
		t.Fatalf("decode a turn: %v", err)
	}
	if turn.Id != "seg-1" || turn.Speaker != "Ann" {
		t.Fatalf("transcript out of order: %+v", turn)
	}

	// An object that does not carry the type holds none of the surfaces.
	other := mustCreateObject(t, e, sp.Id, `{"type":"`+plainType(t, e, sp.Id)+`"}`)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/upsert",
		`{"objectId":"`+other+`","dataset":"`+collections["transcript"]+`","records":[{"id":"x","fields":{"text":"no"}}]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "dataset.not_declared") {
		t.Fatalf("write without the type: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_CatalogSetupWiki pins the one-object shape: the root is
// the wiki COLLECTION (hidden, xKey wiki, three columns by handle) AND
// the miniapp entry, and a second setup adopts it unchanged. A page
// keeps its own type and is filed under the collection.
func TestServer_CatalogSetupWiki(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogWiki")

	res := setupUsecase(t, e, "wiki", sp.Id)
	if res.Usecase != "wiki" || len(res.Bundles) != 1 {
		t.Fatalf("reply: %+v", res)
	}
	b := res.Bundles[0]
	if !b.Installed || b.Id != "system:wiki/v1" || b.Usecase != "wiki" || b.TypeId != "" ||
		b.CollectionId == "" || b.CollectionId != b.Bundle.RootId {
		t.Fatalf("wiki bundle: %+v", b)
	}
	wikiId := b.CollectionId
	for _, xk := range []string{"parentId", "pos", "folder"} {
		if b.Properties[xk] == "" {
			t.Fatalf("property %s unresolved: %+v", xk, b.Properties)
		}
	}
	if b.Miniapp["bundle"] != "system:wiki/v1" {
		t.Fatalf("miniapp values: %+v", b.Miniapp)
	}

	// The root carries the collection marker and is filed under miniapp
	// with the bundle id — and is NOT in its own collection: a
	// self-filed wiki root would be a page in its own tree, sorted
	// among the pages it is the app for.
	row := objectRow(t, e, sp.Id, wikiId)
	if got := rowType(row); got != "__collection__" {
		t.Fatalf("wiki root any.type = %q", got)
	}
	cols := rowCollections(row)
	if !slices.Contains(cols, "miniapp") || slices.Contains(cols, wikiId) {
		t.Fatalf("wiki root collections = %v", cols)
	}
	if ma, _ := row["miniapp"].(map[string]any); ma["bundle"] != "system:wiki/v1" {
		t.Fatalf("miniapp.bundle on the root: %v", row["miniapp"])
	}

	// It is a collection, not a type, on both surfaces.
	rec := doJSON(t, e, http.MethodGet, "/v1/spaces/"+sp.Id+"/types/"+wikiId, "")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "type.not_a_type") {
		t.Fatalf("wiki on the types route: %d %s", rec.Code, rec.Body.String())
	}

	// Hidden: absent from the plain list, present with includeHidden.
	var listed api.CollectionsListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/collections", &listed)
	for _, ci := range listed.Collections {
		if ci.Id == wikiId {
			t.Fatalf("hidden wiki collection listed by default")
		}
	}
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/collections?includeHidden=true", &listed)
	var found *api.CollectionInfo
	for i := range listed.Collections {
		if listed.Collections[i].Id == wikiId {
			found = &listed.Collections[i]
		}
	}
	if found == nil || found.XKey != "wiki" || !found.Hidden || found.Name != "Wiki" {
		t.Fatalf("wiki collection info: %+v", found)
	}

	// Adopt: same ids, nothing installed.
	again := setupUsecase(t, e, "wiki", sp.Id)
	if again.Bundles[0].Installed || again.Bundles[0].CollectionId != wikiId ||
		again.Bundles[0].Properties["pos"] != b.Properties["pos"] {
		t.Fatalf("second setup did not adopt: %+v", again.Bundles[0])
	}

	// A page filed under the wiki takes its columns by id, and the tree
	// is a plain query on them: children of a parent sorted by pos. An
	// object created outside the collection is not in the tree, and
	// nothing stamps a `nav` namespace any more.
	parentProp, posProp, folderProp := b.Properties["parentId"], b.Properties["pos"], b.Properties["folder"]
	mk := func(name, parent, pos string, folder bool) string {
		body := `{"type":"page","collections":["` + wikiId + `"],"initialProperties":{"any":{"name":"` + name + `"},"` + wikiId +
			`":{"` + parentProp + `":"` + parent + `","` + posProp + `":"` + pos + `","` + folderProp + `":` + map[bool]string{true: "true", false: "false"}[folder] + `}}}`
		rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create wiki page %s: %d %s", name, rec.Code, rec.Body.String())
		}
		var out api.ObjectsCreateResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out.ObjectId
	}
	folder := mk("Folder", "", "a0", true)
	second := mk("Second", "", "a1", false)
	inside := mk("Inside", folder, "a0", false)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", `{"type":"page"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bare page: %d %s", rec.Code, rec.Body.String())
	}
	var bare api.ObjectsCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &bare)
	if _, has := objectRow(t, e, sp.Id, bare.ObjectId)["nav"]; has {
		t.Fatalf("a bare object still carries a nav namespace")
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
		`{"filter":{"`+wikiId+`.`+parentProp+`":""},"sort":["`+wikiId+`.`+posProp+`"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("tree query: %d %s", rec.Code, rec.Body.String())
	}
	var q api.QueryResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &q)
	var rootIds []string
	for _, raw := range q.Records {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		rootIds = append(rootIds, m["id"].(string))
	}
	if !slices.Equal(rootIds, []string{folder, second}) {
		t.Fatalf("root level = %v, want [%s %s] (folder first by pos, the bare page and the nested one absent)", rootIds, folder, second)
	}
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
		`{"filter":{"`+wikiId+`.`+parentProp+`":"`+folder+`"}}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &q)
	if len(q.Records) != 1 || !strings.Contains(string(q.Records[0]), inside) {
		t.Fatalf("folder children = %s", rec.Body.String())
	}
	fr := objectRow(t, e, sp.Id, folder)
	if fr[wikiId].(map[string]any)[folderProp] != true {
		t.Fatalf("folder flag not stored: %v", fr[wikiId])
	}
	// The page keeps its own type; the wiki is only where it is filed.
	if rowType(fr) != "page" || !slices.Contains(rowCollections(fr), wikiId) {
		t.Fatalf("wiki page membership: type=%q collections=%v", rowType(fr), rowCollections(fr))
	}
}

func TestServer_CatalogSetupCollectionsAndChat(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogBare")

	// collections: a bare miniapp root — declares nothing, so its
	// any.type is the rootType the catalog names, not a marker.
	res := setupUsecase(t, e, "collections", sp.Id)
	b := res.Bundles[0]
	if !b.Installed || b.TypeId != "" || b.CollectionId != "" || b.Properties != nil ||
		b.Miniapp["bundle"] != "system:collections/v1" {
		t.Fatalf("collections bundle: %+v", b)
	}
	row := objectRow(t, e, sp.Id, b.Bundle.RootId)
	if rowType(row) != page.TypeId || !slices.Contains(rowCollections(row), "miniapp") {
		t.Fatalf("collections root: type=%q collections=%v", rowType(row), rowCollections(row))
	}

	// general chat: derived, hidden, a sidebar entry (miniapp), a chat
	// from the first write.
	res = setupUsecase(t, e, "general-chat", sp.Id)
	chat := res.Bundles[0]
	if !chat.Bundle.Derived || chat.TypeId != chat.Bundle.RootId || chat.Miniapp["bundle"] != "system:general-chat/v1" {
		t.Fatalf("general chat bundle: %+v", chat)
	}
	row = objectRow(t, e, sp.Id, chat.Bundle.RootId)
	if cols := rowCollections(row); !slices.Contains(cols, "miniapp") {
		t.Fatalf("general chat root collections = %v, want miniapp", cols)
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/"+chat.Bundle.RootId+"/chat/messages", `{"text":"hello"}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("chat send on the catalog root: %d %s", rec.Code, rec.Body.String())
	}
	var info api.TypeInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+chat.TypeId, &info)
	if info.XKey != "general_chat" || !info.Hidden {
		t.Fatalf("chat type info: %+v", info)
	}
}

// TestServer_CatalogSetupDependencies pins the walk: crm pulls people,
// contact and contacts first, in declaration order, every type once;
// a role alone pulls only people.
func TestServer_CatalogSetupDependencies(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogCRM")

	res := setupUsecase(t, e, "crm", sp.Id)
	want := []string{"system:profile/v1", "system:person/v2", "system:organization/v2", "system:contact/v1", "system:contacts/v1", "system:deal/v2", "system:crm/v1"}
	if got := bundleIds(res); !slices.Equal(got, want) {
		t.Fatalf("setup order = %v, want %v", got, want)
	}
	byId := map[string]api.CatalogSetupBundle{}
	for _, b := range res.Bundles {
		byId[b.Id] = b
		if !b.Installed {
			t.Fatalf("%s not installed on a fresh space", b.Id)
		}
	}
	if byId["system:person/v2"].Usecase != "people" || byId["system:deal/v2"].Usecase != "crm" {
		t.Fatalf("usecase attribution: %+v", res.Bundles)
	}
	// The mutual relation resolves by handle on both sides.
	if byId["system:person/v2"].Properties["organization"] == "" || byId["system:organization/v2"].Properties["main_contact"] == "" {
		t.Fatalf("relation properties unresolved: %+v %+v", byId["system:person/v2"].Properties, byId["system:organization/v2"].Properties)
	}
	// The format is a TYPE with no columns of its own; an identity, a
	// deal and a role facet are COLLECTIONS, so the reply names
	// collectionId for each.
	profile := byId["system:profile/v1"]
	if profile.TypeId != profile.Bundle.RootId || profile.CollectionId != "" || len(profile.Properties) != 0 {
		t.Fatalf("profile: %+v", profile)
	}
	var profileInfo api.TypeInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+profile.TypeId, &profileInfo)
	if profileInfo.XKey != "profile" || !strings.Contains(string(profileInfo.Layout), `"profile"`) {
		t.Fatalf("profile type info: %+v", profileInfo)
	}
	for _, id := range []string{"system:person/v2", "system:organization/v2", "system:deal/v2", "system:contact/v1"} {
		if b := byId[id]; b.TypeId != "" || b.CollectionId != b.Bundle.RootId {
			t.Fatalf("%s is not a collection: %+v", id, b)
		}
	}
	// A collection names the type a row created inside it gets: a
	// profile for an identity and a facet, nothing (a page) for a deal.
	for id, want := range map[string]any{"system:person/v2": "profile", "system:organization/v2": "profile",
		"system:contact/v1": "profile", "system:deal/v2": nil} {
		var info api.CollectionInfo
		decodeGet(t, e, "/v1/spaces/"+sp.Id+"/collections/"+byId[id].CollectionId, &info)
		if got := info.Meta["defaultType"]; got != want {
			t.Fatalf("%s meta.defaultType = %v, want %v", id, got, want)
		}
	}
	// A person is a profile filed under `person`; filed under a facet as
	// well it takes that facet's columns — initialProperties keyed by
	// each collection.
	facet, person := byId["system:contact/v1"], byId["system:person/v2"]
	if facet.Properties["status"] == "" || person.Properties["email"] == "" {
		t.Fatalf("handles unresolved: %+v %+v", facet.Properties, person.Properties)
	}
	who := mustCreateObject(t, e, sp.Id, `{"type":"`+profile.TypeId+`","collections":["`+person.CollectionId+`","`+facet.CollectionId+
		`"],"initialProperties":{"any":{"name":"Ada"},"`+person.CollectionId+`":{"`+person.Properties["email"]+`":"ada@example.com"},"`+
		facet.CollectionId+`":{"`+facet.Properties["status"]+`":["active"]}}}`)
	whoRow := objectRow(t, e, sp.Id, who)
	cols := rowCollections(whoRow)
	if rowType(whoRow) != profile.TypeId || !slices.Contains(cols, person.CollectionId) || !slices.Contains(cols, facet.CollectionId) {
		t.Fatalf("identity membership: type=%q collections=%v", rowType(whoRow), cols)
	}
	if vals, _ := whoRow[person.CollectionId].(map[string]any); vals[person.Properties["email"]] == nil {
		t.Fatalf("person value not stored: %v", whoRow[person.CollectionId])
	}
	if vals, _ := whoRow[facet.CollectionId].(map[string]any); vals[facet.Properties["status"]] == nil {
		t.Fatalf("facet value not stored: %v", whoRow[facet.CollectionId])
	}
	// A profile's body is the shared editor collection.
	rec0 := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/"+who+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"met at the conference"}`)
	if rec0.Code != http.StatusCreated && rec0.Code != http.StatusOK {
		t.Fatalf("write a profile's notes: %d %s", rec0.Code, rec0.Body.String())
	}

	// The app root is hidden and its layouts collection is writable.
	app := byId["system:contacts/v1"]
	var parts api.TypePartsListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+app.TypeId+"/parts", &parts)
	if len(parts.Parts) != 1 || len(parts.Parts[0].Datasets) != 1 {
		t.Fatalf("contacts app parts: %+v", parts)
	}
	coll := parts.Parts[0].Datasets[0].Collection
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/upsert",
		`{"objectId":"`+app.TypeId+`","dataset":"`+coll+`","records":[{"id":"person","fields":{"blocks":["a","b"]}}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert layouts: %d %s", rec.Code, rec.Body.String())
	}

	// Idempotent: the same walk adopts everything.
	again := setupUsecase(t, e, "crm", sp.Id)
	for _, b := range again.Bundles {
		if b.Installed || b.Bundle.RootId != byId[b.Id].Bundle.RootId {
			t.Fatalf("second setup re-installed %s: %+v", b.Id, b)
		}
	}

	// A role alone: people plus the role, no other role.
	sp2 := createSpaceInfo(t, e, "CatalogInvestor")
	res = setupUsecase(t, e, "investor", sp2.Id)
	if got := bundleIds(res); !slices.Equal(got, []string{"system:profile/v1", "system:person/v2", "system:organization/v2", "system:investor/v1"}) {
		t.Fatalf("investor setup order = %v", got)
	}
}

func TestServer_CatalogSetupValidation(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogValidation")

	if rec := doJSON(t, e, http.MethodPost, "/v1/catalog/nope/setup", `{"spaceId":"`+sp.Id+`"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown usecase: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, e, http.MethodPost, "/v1/catalog/wiki/setup", `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing spaceId: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, e, http.MethodPost, "/v1/catalog/wiki/setup", `{"spaceId":"nosuchspace"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown space: %d %s", rec.Code, rec.Body.String())
	}
	var acct api.AccountResponse
	decodeGet(t, e, "/v1/account", &acct)
	if rec := doJSON(t, e, http.MethodPost, "/v1/catalog/wiki/setup", `{"spaceId":"`+acct.TechSpaceId+`"}`); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("tech space: %d %s", rec.Code, rec.Body.String())
	}
	// The system prefix stays the server's on the per-space route.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/bundles", `{"id":"system:wiki/v1","xKey":"wiki2"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.ErrBundleReserved) {
		t.Fatalf("reserved id: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_CatalogSetupXKeyConflict pins the install-path rule: a
// user type already holding a catalog handle refuses the setup and
// names itself; a re-setup of an installed usecase never conflicts
// with its own root.
func TestServer_CatalogSetupXKeyConflict(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogXKey")

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/types", `{"name":"My contact","xKey":"contact"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user type: %d %s", rec.Code, rec.Body.String())
	}
	var created api.TypesCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	rec = doJSON(t, e, http.MethodPost, "/v1/catalog/contact/setup", `{"spaceId":"`+sp.Id+`"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "type.xkey_conflict") ||
		!strings.Contains(rec.Body.String(), created.TypeId) || !strings.Contains(rec.Body.String(), `"usecase":"contact"`) {
		t.Fatalf("xKey conflict: %d %s", rec.Code, rec.Body.String())
	}
	// Dependencies before the conflicting one stayed installed: people
	// is set up, contact is not.
	var bl api.BundleListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/bundles", &bl)
	var ids []string
	for _, b := range bl.Bundles {
		ids = append(ids, b.Id)
	}
	if !slices.Contains(ids, "system:person/v2") || slices.Contains(ids, "system:contact/v1") {
		t.Fatalf("bundles after the refusal: %v", ids)
	}

	// The same rule on the per-space ensure route.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/bundles", `{"id":"mine/v1","xKey":"contact"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "type.xkey_conflict") {
		t.Fatalf("bundle xKey conflict: %d %s", rec.Code, rec.Body.String())
	}
	// A space without the user type sets the usecase up, twice, on one
	// root: an adopted install never conflicts with itself.
	sp2 := createSpaceInfo(t, e, "CatalogXKeyFree")
	first := setupUsecase(t, e, "contact", sp2.Id)
	second := setupUsecase(t, e, "contact", sp2.Id)
	if first.Bundles[len(first.Bundles)-1].Bundle.RootId != second.Bundles[len(second.Bundles)-1].Bundle.RootId {
		t.Fatalf("re-setup minted a second root")
	}
	// And a bundle xKey that is free installs a marker type.
	out := ensureBundle(t, e, sp2.Id, `{"id":"flag/v1","name":"Flag","xKey":"flag","hidden":true}`)
	if !out.Installed {
		t.Fatalf("marker bundle: %+v", out)
	}
	var info api.TypeInfo
	decodeGet(t, e, "/v1/spaces/"+sp2.Id+"/types/"+out.Bundle.RootId, &info)
	if info.XKey != "flag" || !info.Hidden {
		t.Fatalf("marker type: %+v", info)
	}
}

// The evolution rules, on a catalog a test owns: a property added to
// a type, a miniapp added to a bundle, a bundle added to a usecase,
// each landing on the existing install at the next setup — and a
// marker type, a two-level chain and a relation across it.
const testCatalogV1 = `
usecases:
  - id: seam-tag
    name: Tag
    bundles:
      - id: system:seam-tag/v1
        name: Tag
        type: { xKey: seam_tag, layout: { type: page } }
  - id: seam-note
    name: Note
    requires: [ seam-tag ]
    bundles:
      - id: system:seam-note/v1
        name: Note
        type:
          xKey: seam_note
          layout: { type: page }
          properties:
            - { xKey: title, name: Title, kind: string }
            - { xKey: tags,  name: Tags,  kind: array,
                xFormat: { type: relation, relation: { targetTypes: [ seam_tag ] }, config: { multiple: true } } }
            - { xKey: stage, name: Stage, kind: array,
                xFormat: { type: choice, options: { open: { name: Open, pos: a0 } } } }
  - id: seam-app
    name: App
    requires: [ seam-note ]
    bundles:
      - id: system:seam-app/v1
        name: App
        parts:
          - key: state
            datasets: [ { key: state, idRule: user, fields: [ { key: v, kind: string, mutableBy: any } ] } ]
`

const testCatalogV2 = `
usecases:
  - id: seam-tag
    name: Tag
    bundles:
      - id: system:seam-tag/v1
        name: Tag
        type: { xKey: seam_tag, layout: { type: page } }
  - id: seam-note
    name: Note
    requires: [ seam-tag ]
    bundles:
      - id: system:seam-note/v1
        name: Note
        type:
          xKey: seam_note
          layout: { type: page }
          properties:
            - { xKey: title, name: Title, kind: string }
            - { xKey: tags,  name: Tags,  kind: array,
                xFormat: { type: relation, relation: { targetTypes: [ seam_tag ] }, config: { multiple: true } } }
            - { xKey: stage, name: Stage, kind: array,
                xFormat: { type: choice, options: { open: { name: Open, pos: a0 }, closed: { name: Closed, pos: a1 } } } }
            - { xKey: done,  name: Done,  kind: boolean, xFormat: { type: checkbox } }
  - id: seam-app
    name: App
    requires: [ seam-note ]
    bundles:
      - id: system:seam-app/v1
        name: App
        miniapp: {}
        parts:
          - key: state
            datasets: [ { key: state, idRule: user, fields: [ { key: v, kind: string, mutableBy: any } ] } ]
      - id: system:seam-app-extra/v1
        name: Extra
        rootType: page
        miniapp: {}
`

func TestServer_CatalogEvolution(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogEvolution")

	d.catalog = catalogForTest(t, testCatalogV1)
	v1 := setupUsecase(t, e, "seam-app", sp.Id)
	if got := bundleIds(v1); !slices.Equal(got, []string{"system:seam-tag/v1", "system:seam-note/v1", "system:seam-app/v1"}) {
		t.Fatalf("v1 order = %v", got)
	}
	tag, note, app := v1.Bundles[0], v1.Bundles[1], v1.Bundles[2]
	// A marker type: an xKey alone, resolvable, no columns; objects carry it.
	if tag.TypeId == "" || len(tag.Properties) != 0 {
		t.Fatalf("marker type: %+v", tag)
	}
	var info api.TypeInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+tag.TypeId, &info)
	if info.XKey != "seam_tag" {
		t.Fatalf("marker info: %+v", info)
	}
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", `{"type":"`+tag.TypeId+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("carry marker: %d %s", rec.Code, rec.Body.String())
	}
	if len(note.Properties) != 3 || app.Miniapp != nil || app.TypeId == "" {
		t.Fatalf("v1 note/app: %+v %+v", note, app)
	}
	// The space recolours an installed option: that leaf is the
	// space's own and must survive the heal below.
	rec = doJSON(t, e, http.MethodPatch, "/v1/spaces/"+sp.Id+"/types/"+note.TypeId+"/properties/"+note.Properties["stage"],
		`{"set":{"xFormat.options.open.color":"green"}}`)
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Fatalf("recolour option: %d %s", rec.Code, rec.Body.String())
	}

	// The catalog evolves; the same space is set up again.
	d.catalog = catalogForTest(t, testCatalogV2)
	v2 := setupUsecase(t, e, "seam-app", sp.Id)
	if got := bundleIds(v2); !slices.Equal(got, []string{"system:seam-tag/v1", "system:seam-note/v1", "system:seam-app/v1", "system:seam-app-extra/v1"}) {
		t.Fatalf("v2 order = %v", got)
	}
	note2, app2, extra := v2.Bundles[1], v2.Bundles[2], v2.Bundles[3]
	// The added property healed onto the existing root, ids stable.
	if note2.Installed || note2.TypeId != note.TypeId || note2.Properties["title"] != note.Properties["title"] || note2.Properties["done"] == "" {
		t.Fatalf("property heal: %+v", note2)
	}
	// The added option key healed onto the installed choice; the
	// space's recolour of the existing key stands.
	var defs api.PropertiesListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+note.TypeId+"/properties", &defs)
	var stage *api.PropertyDef
	for i := range defs.Properties {
		if defs.Properties[i].XKey == "stage" {
			stage = &defs.Properties[i]
		}
	}
	if stage == nil {
		t.Fatalf("stage property missing: %+v", defs)
	}
	var xf struct {
		Options map[string]map[string]any `json:"options"`
	}
	if err := json.Unmarshal(stage.XFormat, &xf); err != nil {
		t.Fatalf("decode xFormat: %v %s", err, stage.XFormat)
	}
	if xf.Options["closed"]["name"] != "Closed" || xf.Options["closed"]["pos"] != "a1" {
		t.Fatalf("option heal: %+v", xf.Options)
	}
	if xf.Options["open"]["color"] != "green" || xf.Options["open"]["name"] != "Open" {
		t.Fatalf("existing option touched: %+v", xf.Options["open"])
	}
	// The added miniapp values landed on the adopted root.
	if app2.Installed || app2.TypeId != app.TypeId || app2.Miniapp["bundle"] != "system:seam-app/v1" {
		t.Fatalf("miniapp heal reply: %+v", app2)
	}
	row := objectRow(t, e, sp.Id, app2.TypeId)
	if !slices.Contains(rowCollections(row), "miniapp") {
		t.Fatalf("miniapp not attached on heal: %v", rowCollections(row))
	}
	if ma, _ := row["miniapp"].(map[string]any); ma["bundle"] != "system:seam-app/v1" {
		t.Fatalf("miniapp.bundle not healed: %v", row["miniapp"])
	}
	// The added bundle installed.
	if !extra.Installed || extra.Miniapp["bundle"] != "system:seam-app-extra/v1" {
		t.Fatalf("added bundle: %+v", extra)
	}
	// A third setup changes nothing.
	v3 := setupUsecase(t, e, "seam-app", sp.Id)
	for _, b := range v3.Bundles {
		if b.Installed {
			t.Fatalf("third setup installed %s", b.Id)
		}
	}
}

// The shape a definition takes when it changes kind: `seam_item` was a
// type and becomes a collection of a new `seam_card` format. The old
// bundle stays declared, and the two that stand in for it supersede it.
const testCatalogSuperseding = `
usecases:
  - id: seam-items
    name: Items
    bundles:
      - id: system:seam-card/v1
        name: Card
        supersedes: [ system:seam-item/v1 ]
        type: { xKey: seam_card, layout: { type: profile } }
      - id: system:seam-item/v2
        name: Items
        supersedes: [ system:seam-item/v1 ]
        collection:
          xKey: seam_item
          meta: { defaultType: seam_card }
          properties:
            - { xKey: title, name: Title, kind: string }
      - id: system:seam-item/v1
        name: Item
        type:
          xKey: seam_item
          properties:
            - { xKey: title, name: Title, kind: string }
            - { xKey: note,  name: Note,  kind: string }
`

const testCatalogBeforeSuperseding = `
usecases:
  - id: seam-items
    name: Items
    bundles:
      - id: system:seam-item/v1
        name: Item
        type:
          xKey: seam_item
          layout: { type: page }
          properties:
            - { xKey: title, name: Title, kind: string }
`

// TestServer_CatalogSuperseded pins the two halves of `supersedes`: a
// space that has the superseded bundle keeps it — adopted, healed, and
// never joined by what supersedes it — while every other space receives
// the new bundles and never the old one. The two share a handle, so
// installing both into one space would be a conflict; neither walk hits it.
func TestServer_CatalogSuperseded(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	had := createSpaceInfo(t, e, "CatalogHadTheType")
	d.catalog = catalogForTest(t, testCatalogBeforeSuperseding)
	before := setupUsecase(t, e, "seam-items", had.Id)
	if got := bundleIds(before); !slices.Equal(got, []string{"system:seam-item/v1"}) || !before.Bundles[0].Installed {
		t.Fatalf("the type, installed: %+v", before)
	}
	item := before.Bundles[0]

	d.catalog = catalogForTest(t, testCatalogSuperseding)
	kept := setupUsecase(t, e, "seam-items", had.Id)
	if got := bundleIds(kept); !slices.Equal(got, []string{"system:seam-item/v1"}) {
		t.Fatalf("a space that has the type keeps it and nothing else: %v", got)
	}
	if b := kept.Bundles[0]; b.Installed || b.TypeId != item.TypeId || b.CollectionId != "" {
		t.Fatalf("the kept bundle is adopted as the type it is: %+v", b)
	}
	// Kept is not frozen: a property the declaration gained still heals.
	if kept.Bundles[0].Properties["note"] == "" {
		t.Fatalf("the superseded bundle still heals: %+v", kept.Bundles[0].Properties)
	}
	var bl api.BundleListResponse
	decodeGet(t, e, "/v1/spaces/"+had.Id+"/bundles", &bl)
	for _, b := range bl.Bundles {
		if b.Id == "system:seam-item/v2" || b.Id == "system:seam-card/v1" {
			t.Fatalf("%s reached a space that has the bundle it supersedes", b.Id)
		}
	}

	fresh := createSpaceInfo(t, e, "CatalogNeverHadIt")
	got := setupUsecase(t, e, "seam-items", fresh.Id)
	if ids := bundleIds(got); !slices.Equal(ids, []string{"system:seam-card/v1", "system:seam-item/v2"}) {
		t.Fatalf("a new space receives what supersedes, never the superseded: %v", ids)
	}
	card, items := got.Bundles[0], got.Bundles[1]
	if card.TypeId == "" || items.CollectionId == "" || items.TypeId != "" {
		t.Fatalf("a format and a collection: %+v %+v", card, items)
	}
	// The declared flag is written on install, which carries no meta of
	// its own, and a second setup leaves a value the space changed alone.
	var info api.CollectionInfo
	decodeGet(t, e, "/v1/spaces/"+fresh.Id+"/collections/"+items.CollectionId, &info)
	if info.Meta["defaultType"] != "seam_card" {
		t.Fatalf("meta after install: %+v", info.Meta)
	}
	rec := doJSON(t, e, http.MethodPatch, "/v1/spaces/"+fresh.Id+"/collections/"+items.CollectionId, `{"meta":{"defaultType":"page"}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch meta: %d %s", rec.Code, rec.Body.String())
	}
	again := setupUsecase(t, e, "seam-items", fresh.Id)
	if ids := bundleIds(again); !slices.Equal(ids, []string{"system:seam-card/v1", "system:seam-item/v2"}) || again.Bundles[1].Installed {
		t.Fatalf("second setup adopts the same two: %+v", again)
	}
	decodeGet(t, e, "/v1/spaces/"+fresh.Id+"/collections/"+items.CollectionId, &info)
	if info.Meta["defaultType"] != "page" {
		t.Fatalf("setup overwrote a flag the space set: %+v", info.Meta)
	}
	// A cleared key stays in the bag as "", so the next setup does not
	// seed the declared value again.
	rec = doJSON(t, e, http.MethodPatch, "/v1/spaces/"+fresh.Id+"/collections/"+items.CollectionId, `{"meta":{"defaultType":null}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear meta: %d %s", rec.Code, rec.Body.String())
	}
	decodeGet(t, e, "/v1/spaces/"+fresh.Id+"/collections/"+items.CollectionId, &info)
	if v, ok := info.Meta["defaultType"]; !ok || v != "" {
		t.Fatalf("cleared key: %+v", info.Meta)
	}
	setupUsecase(t, e, "seam-items", fresh.Id)
	decodeGet(t, e, "/v1/spaces/"+fresh.Id+"/collections/"+items.CollectionId, &info)
	if v, ok := info.Meta["defaultType"]; !ok || v != "" {
		t.Fatalf("setup seeded a flag the space cleared: %+v", info.Meta)
	}
	// A clear is a PATCH; create takes values only.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+fresh.Id+"/collections", `{"xKey":"seam_nulls","meta":{"defaultType":null}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create with a null meta value: %d %s", rec.Code, rec.Body.String())
	}
}

// Two types before the change; after it one format supersedes both and
// each type becomes a collection of that format.
const testCatalogTwoTypes = `
usecases:
  - id: seam-people
    name: People
    bundles:
      - id: system:seam-person/v1
        name: Person
        type: { xKey: seam_person, layout: { type: profile } }
      - id: system:seam-firm/v1
        name: Firm
        type: { xKey: seam_firm, layout: { type: profile } }
`

const testCatalogOneFormat = `
usecases:
  - id: seam-people
    name: People
    bundles:
      - id: system:seam-face/v1
        name: Face
        supersedes: [ system:seam-person/v1, system:seam-firm/v1 ]
        type: { xKey: seam_face, layout: { type: profile } }
      - id: system:seam-person/v2
        name: People
        supersedes: [ system:seam-person/v1 ]
        collection: { xKey: seam_person, meta: { defaultType: seam_face } }
      - id: system:seam-firm/v2
        name: Firms
        supersedes: [ system:seam-firm/v1 ]
        collection: { xKey: seam_firm, meta: { defaultType: seam_face } }
      - id: system:seam-person/v1
        name: Person
        type: { xKey: seam_person, layout: { type: profile } }
      - id: system:seam-firm/v1
        name: Firm
        type: { xKey: seam_firm, layout: { type: profile } }
`

// TestServer_CatalogSupersedeGroup pins that bundles linked by
// `supersedes` are decided as one: a space with any old bundle left
// stays on the old shape — an uninstalled old bundle comes back rather
// than half of the new set — and moves to the new shape only once the
// whole old set is gone. The listing marks the old bundles.
func TestServer_CatalogSupersedeGroup(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "CatalogTwoTypes")
	d.catalog = catalogForTest(t, testCatalogTwoTypes)
	first := setupUsecase(t, e, "seam-people", sp.Id)
	if got := bundleIds(first); !slices.Equal(got, []string{"system:seam-person/v1", "system:seam-firm/v1"}) {
		t.Fatalf("two types: %v", got)
	}
	person, firm := first.Bundles[0], first.Bundles[1]

	d.catalog = catalogForTest(t, testCatalogOneFormat)
	kept := setupUsecase(t, e, "seam-people", sp.Id)
	if got := bundleIds(kept); !slices.Equal(got, []string{"system:seam-person/v1", "system:seam-firm/v1"}) ||
		kept.Bundles[0].Installed || kept.Bundles[1].Installed {
		t.Fatalf("both kept: %+v", kept)
	}

	// One old bundle uninstalled: the space is still on the old shape,
	// so the bundle is minted again and nothing of the new set arrives.
	if rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+sp.Id+"/objects/"+person.TypeId, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("uninstall person: %d %s", rec.Code, rec.Body.String())
	}
	back := setupUsecase(t, e, "seam-people", sp.Id)
	if got := bundleIds(back); !slices.Equal(got, []string{"system:seam-person/v1", "system:seam-firm/v1"}) {
		t.Fatalf("old shape after one uninstall: %v", got)
	}
	if b := back.Bundles[0]; !b.Installed || b.TypeId == "" || b.TypeId == person.TypeId {
		t.Fatalf("person minted again: %+v", b)
	}
	if back.Bundles[1].Installed || back.Bundles[1].TypeId != firm.TypeId {
		t.Fatalf("firm kept: %+v", back.Bundles[1])
	}

	// The whole old set gone: the next setup installs the new one.
	for _, id := range []string{back.Bundles[0].TypeId, firm.TypeId} {
		if rec := doJSON(t, e, http.MethodDelete, "/v1/spaces/"+sp.Id+"/objects/"+id, ""); rec.Code != http.StatusNoContent {
			t.Fatalf("uninstall %s: %d %s", id, rec.Code, rec.Body.String())
		}
	}
	moved := setupUsecase(t, e, "seam-people", sp.Id)
	if got := bundleIds(moved); !slices.Equal(got, []string{"system:seam-face/v1", "system:seam-person/v2", "system:seam-firm/v2"}) {
		t.Fatalf("new shape: %v", got)
	}
	for _, b := range moved.Bundles {
		if !b.Installed {
			t.Fatalf("new set installed: %+v", b)
		}
	}
	if moved.Bundles[0].TypeId == "" || moved.Bundles[1].CollectionId == "" || moved.Bundles[2].CollectionId == "" {
		t.Fatalf("a format and two collections: %+v", moved.Bundles)
	}
	var info api.CollectionInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/collections/"+moved.Bundles[1].CollectionId, &info)
	if info.Meta["defaultType"] != "seam_face" {
		t.Fatalf("meta on install: %+v", info.Meta)
	}

	var u api.CatalogUsecase
	decodeGet(t, e, "/v1/catalog/seam-people", &u)
	flags := map[string]bool{}
	for _, b := range u.Bundles {
		flags[b.Id] = b.Superseded
	}
	if !flags["system:seam-person/v1"] || !flags["system:seam-firm/v1"] || flags["system:seam-face/v1"] || flags["system:seam-person/v2"] {
		t.Fatalf("listing flags: %v", flags)
	}
}

// TestServer_CatalogValidateEmbedded is the build gate in test form:
// the embedded catalog compiles through the server's own gate.
func TestServer_CatalogValidateEmbedded(t *testing.T) {
	if problems := ValidateCatalog(catalog.Embedded()); len(problems) > 0 {
		t.Fatalf("embedded catalog: %v", problems)
	}
	// The server layer catches what the pure layer cannot: a slug on
	// the wrong kind, a miniapp key the built-in lacks, an xKey equal
	// to a registered type id.
	bad := strings.Replace(testCatalogV2, "kind: string }", "kind: string, xFormat: { type: money } }", 1)
	bad = strings.Replace(bad, "miniapp: {}", "miniapp: { entry: index.html }", 1)
	bad = strings.Replace(bad, "xKey: seam_tag,", "xKey: page,", 1)
	problems := ValidateCatalog([]byte(bad))
	var codes []string
	for _, p := range problems {
		codes = append(codes, p.Code+"@"+p.Path)
	}
	joined := strings.Join(codes, "\n")
	for _, want := range []string{
		"property.format_invalid@usecases[1].bundles[0].type.properties[0]",
		"catalog.duplicate@usecases[0].bundles[0].type.xKey",
		"catalog.bad_miniapp@usecases[2].bundles[0].miniapp.entry",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in:\n%s", want, joined)
		}
	}
	// A mapping with a non-string key is reported where it sits, with
	// a path, not as an opaque bridge error.
	numeric := strings.Replace(testCatalogV1, "open: { name: Open, pos: a0 }", "1: { name: One, pos: a0 }", 1)
	found := false
	for _, p := range ValidateCatalog([]byte(numeric)) {
		if p.Code == catalog.CodeBadField && strings.Contains(p.Message, "quote") && p.Path != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("numeric option key not reported with a path: %v", ValidateCatalog([]byte(numeric)))
	}
}

// TestServer_CatalogSetupEveryUsecase installs every shipped usecase
// into one space: the build gate does not run the SDK's own draft
// validators, so this is where a shipped part or property draft the
// SDK would refuse surfaces. Every bundle ends up installed exactly
// once across the walks.
func TestServer_CatalogSetupEveryUsecase(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogAll")

	var list api.CatalogListResponse
	decodeGet(t, e, "/v1/catalog", &list)
	roots := map[string]string{}
	for _, u := range list.Usecases {
		res := setupUsecase(t, e, u.Id, sp.Id)
		for _, b := range res.Bundles {
			if prev, seen := roots[b.Id]; seen {
				if b.Installed || prev != b.Bundle.RootId {
					t.Fatalf("%s re-installed during %s: %+v", b.Id, u.Id, b)
				}
				continue
			}
			if !b.Installed {
				t.Fatalf("%s not installed on first sight during %s: %+v", b.Id, u.Id, b)
			}
			roots[b.Id] = b.Bundle.RootId
		}
	}
	var bl api.BundleListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/bundles", &bl)
	if len(bl.Bundles) != len(roots) {
		t.Fatalf("registry has %d rows, setup touched %d bundles", len(bl.Bundles), len(roots))
	}
	// The app roots that declare nothing are plain documents: the
	// catalog's rootType is what setup mints them with.
	for _, id := range []string{"system:collections/v1", "system:meetings/v1", "system:tasks/v1", "system:crm/v1"} {
		root, ok := roots[id]
		if !ok {
			t.Fatalf("%s was not installed", id)
		}
		if got := rowType(objectRow(t, e, sp.Id, root)); got != page.TypeId {
			t.Errorf("%s root any.type = %q, want %q", id, got, page.TypeId)
		}
	}
}
