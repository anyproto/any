package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/catalog"
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

func rowTypes(row map[string]any) []string {
	anyNs, _ := row["any"].(map[string]any)
	list, _ := anyNs["types"].([]any)
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
	if len(wiki.Bundles) != 1 || wiki.Bundles[0].Id != "system:wiki/v1" || wiki.Bundles[0].Type == nil ||
		wiki.Bundles[0].Type.XKey != "wiki" || !wiki.Bundles[0].Hidden {
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

	for _, id := range []string{"journal", "meetings"} {
		t.Run(id, func(t *testing.T) {
			res := setupUsecase(t, e, id, sp.Id)
			if res.Usecase != id {
				t.Fatalf("setup reply: %+v", res)
			}
			bundleId := "system:" + id + "/v1"
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

// Journal brings its type: one root that is the sidebar entry AND the
// hidden `journal` type, whose entries are dated pages in the shared
// editor collection.
func TestServer_CatalogSetupJournal(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogJournal")

	b := setupUsecase(t, e, "journal", sp.Id).Bundles[0]
	if b.TypeId != b.Bundle.RootId || b.Properties["date"] == "" {
		t.Fatalf("journal bundle: %+v", b)
	}
	// The root is the type DEFINITION plus the sidebar entry — it must
	// not carry the type, or the app itself would read as an entry.
	types := rowTypes(objectRow(t, e, sp.Id, b.TypeId))
	if !slices.Contains(types, "__type__") || !slices.Contains(types, "miniapp") || slices.Contains(types, b.TypeId) {
		t.Fatalf("journal root types = %v", types)
	}
	var info api.TypeInfo
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types/"+b.TypeId, &info)
	if info.XKey != "journal" || !info.Hidden || info.Weight != 0 {
		t.Fatalf("journal type info: %+v", info)
	}

	// An entry is one object: the type carries the day, and its part
	// shares the editor collection, so the body needs no second type.
	day := `{"$date":"2026-09-12T00:00:00.000Z"}`
	entry := mustCreateObject(t, e, sp.Id, `{"types":["`+b.TypeId+`"],"initialProperties":{"`+
		b.TypeId+`":{"`+b.Properties["date"]+`":`+day+`}}}`)
	rec := doJSON(t, e, http.MethodPost,
		"/v1/spaces/"+sp.Id+"/objects/"+entry+"/editor/editor_blocks/blocks",
		`{"type":"paragraph","text":"woke up"}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("write the entry's body: %d %s", rec.Code, rec.Body.String())
	}
	// The day is the query key the Journal surface reads by.
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
		`{"filter":{"any.types":"`+b.TypeId+`","`+b.TypeId+`.`+b.Properties["date"]+`":`+day+`}}`)
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
	if info.XKey != "meeting" || info.Hidden || info.Weight != 20 {
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

	obj := mustCreateObject(t, e, sp.Id, `{"types":["`+meeting.TypeId+`"],"initialProperties":{"any":{"name":"Weekly sync"},"`+
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
	other := mustCreateObject(t, e, sp.Id, `{}`)
	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/upsert",
		`{"objectId":"`+other+`","dataset":"`+collections["transcript"]+`","records":[{"id":"x","fields":{"text":"no"}}]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "dataset.not_declared") {
		t.Fatalf("write without the type: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_CatalogSetupWiki pins the one-object shape: the root is
// the wiki type (hidden, xKey wiki, three columns by handle) AND the
// miniapp carrier, and a second setup adopts it unchanged.
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
	if !b.Installed || b.Id != "system:wiki/v1" || b.Usecase != "wiki" || b.TypeId == "" || b.TypeId != b.Bundle.RootId {
		t.Fatalf("wiki bundle: %+v", b)
	}
	for _, xk := range []string{"parentId", "pos", "folder"} {
		if b.Properties[xk] == "" {
			t.Fatalf("property %s unresolved: %+v", xk, b.Properties)
		}
	}
	if b.Miniapp["bundle"] != "system:wiki/v1" {
		t.Fatalf("miniapp values: %+v", b.Miniapp)
	}

	// The root carries the marker and miniapp, with the bundle id — and
	// NOT its own type: a self-typed wiki root would be a page in its
	// own tree, sorted among the pages it is the app for.
	row := objectRow(t, e, sp.Id, b.TypeId)
	types := rowTypes(row)
	for _, want := range []string{"__type__", "miniapp"} {
		if !slices.Contains(types, want) {
			t.Fatalf("root types %v lack %s", types, want)
		}
	}
	if slices.Contains(types, b.TypeId) {
		t.Fatalf("wiki root carries its own type: %v", types)
	}
	if ma, _ := row["miniapp"].(map[string]any); ma["bundle"] != "system:wiki/v1" {
		t.Fatalf("miniapp.bundle on the root: %v", row["miniapp"])
	}

	// Hidden: absent from the plain list, present with includeHidden,
	// resolvable by xKey, no weight.
	var listed api.TypesListResponse
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types", &listed)
	for _, ti := range listed.Types {
		if ti.Id == b.TypeId {
			t.Fatalf("hidden wiki type listed by default")
		}
	}
	decodeGet(t, e, "/v1/spaces/"+sp.Id+"/types?includeHidden=true", &listed)
	var found *api.TypeInfo
	for i := range listed.Types {
		if listed.Types[i].Id == b.TypeId {
			found = &listed.Types[i]
		}
	}
	if found == nil || found.XKey != "wiki" || !found.Hidden || found.Weight != 0 || found.Name != "Wiki" {
		t.Fatalf("wiki type info: %+v", found)
	}

	// Adopt: same ids, nothing installed.
	again := setupUsecase(t, e, "wiki", sp.Id)
	if again.Bundles[0].Installed || again.Bundles[0].TypeId != b.TypeId ||
		again.Bundles[0].Properties["pos"] != b.Properties["pos"] {
		t.Fatalf("second setup did not adopt: %+v", again.Bundles[0])
	}

	// A page carrying the wiki type takes its columns by id, and the
	// tree is a plain query on them: children of a parent sorted by
	// pos. An object created without the type is not in the tree, and
	// nothing stamps a `nav` namespace any more.
	parentProp, posProp, folderProp := b.Properties["parentId"], b.Properties["pos"], b.Properties["folder"]
	mk := func(name, parent, pos string, folder bool) string {
		body := `{"types":["page","` + b.TypeId + `"],"initialProperties":{"any":{"name":"` + name + `"},"` + b.TypeId +
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
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", `{"types":["page"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create bare page: %d %s", rec.Code, rec.Body.String())
	}
	var bare api.ObjectsCreateResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &bare)
	if _, has := objectRow(t, e, sp.Id, bare.ObjectId)["nav"]; has {
		t.Fatalf("a bare object still carries a nav namespace")
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects/query",
		`{"filter":{"`+b.TypeId+`.`+parentProp+`":""},"sort":["`+b.TypeId+`.`+posProp+`"]}`)
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
		`{"filter":{"`+b.TypeId+`.`+parentProp+`":"`+folder+`"}}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &q)
	if len(q.Records) != 1 || !strings.Contains(string(q.Records[0]), inside) {
		t.Fatalf("folder children = %s", rec.Body.String())
	}
	if fr := objectRow(t, e, sp.Id, folder); fr[b.TypeId].(map[string]any)[folderProp] != true {
		t.Fatalf("folder flag not stored: %v", fr[b.TypeId])
	}
}

func TestServer_CatalogSetupCollectionsAndChat(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	sp := createSpaceInfo(t, e, "CatalogBare")

	// collections: a bare miniapp root — no type, the marker with the id.
	res := setupUsecase(t, e, "collections", sp.Id)
	b := res.Bundles[0]
	if !b.Installed || b.TypeId != "" || b.Properties != nil || b.Miniapp["bundle"] != "system:collections/v1" {
		t.Fatalf("collections bundle: %+v", b)
	}
	types := rowTypes(objectRow(t, e, sp.Id, b.Bundle.RootId))
	if !slices.Contains(types, "miniapp") || slices.Contains(types, "__type__") {
		t.Fatalf("collections root types: %v", types)
	}

	// general chat: derived, hidden, a sidebar entry (miniapp), a chat
	// from the first write.
	res = setupUsecase(t, e, "general-chat", sp.Id)
	chat := res.Bundles[0]
	if !chat.Bundle.Derived || chat.TypeId != chat.Bundle.RootId || chat.Miniapp["bundle"] != "system:general-chat/v1" {
		t.Fatalf("general chat bundle: %+v", chat)
	}
	row := objectRow(t, e, sp.Id, chat.Bundle.RootId)
	if types := rowTypes(row); !slices.Contains(types, "miniapp") {
		t.Fatalf("general chat root types = %v, want miniapp", types)
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
	want := []string{"system:person/v1", "system:organization/v1", "system:contact/v1", "system:contacts/v1", "system:deal/v1", "system:crm/v1"}
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
	if byId["system:person/v1"].Usecase != "people" || byId["system:deal/v1"].Usecase != "crm" {
		t.Fatalf("usecase attribution: %+v", res.Bundles)
	}
	// The mutual relation resolves by handle on both sides.
	if byId["system:person/v1"].Properties["organization"] == "" || byId["system:organization/v1"].Properties["main_contact"] == "" {
		t.Fatalf("relation properties unresolved: %+v %+v", byId["system:person/v1"].Properties, byId["system:organization/v1"].Properties)
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
	if got := bundleIds(res); !slices.Equal(got, []string{"system:person/v1", "system:organization/v1", "system:investor/v1"}) {
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
	if !slices.Contains(ids, "system:person/v1") || slices.Contains(ids, "system:contact/v1") {
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
        type: { xKey: seam_tag }
  - id: seam-note
    name: Note
    requires: [ seam-tag ]
    bundles:
      - id: system:seam-note/v1
        name: Note
        type:
          xKey: seam_note
          weight: 10
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
        type: { xKey: seam_tag }
  - id: seam-note
    name: Note
    requires: [ seam-tag ]
    bundles:
      - id: system:seam-note/v1
        name: Note
        type:
          xKey: seam_note
          weight: 10
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
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+sp.Id+"/objects", `{"types":["`+tag.TypeId+`"]}`)
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
	if !slices.Contains(rowTypes(row), "miniapp") {
		t.Fatalf("miniapp not attached on heal: %v", rowTypes(row))
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
	bad = strings.Replace(bad, "xKey: seam_tag }", "xKey: page }", 1)
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
}
