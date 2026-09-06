package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anyproto/any-sync-sdk/handler"

	"github.com/anyproto/any/internal/api"
)

// The compiled-in catalog seam: a hidden registered type with two
// static records datasets under two parts (one hidden), and a reserved
// module. Registered once for the test binary — the server's catalog
// is process-wide, and every test tolerates two extra entries (the type
// is hidden, the module's canonical is one more discovery row).
func init() {
	extraCatalog.types = append(extraCatalog.types, handler.Type{
		Id: "testdoc", Name: "Test document", Hidden: true,
		Datasets: []handler.Dataset{
			{Name: "testdoc_notes", DataVersion: "testdoc_notes-v1",
				Schema: handler.Schema{Fields: []handler.Field{
					{Id: "title", Schema: handler.Leaf(handler.PropertyKindString), MutableBy: handler.MutableByAnyone},
				}}},
			{Name: "testdoc_log", DataVersion: "testdoc_log-v1",
				Schema: handler.Schema{Fields: []handler.Field{
					{Id: "text", Schema: handler.Leaf(handler.PropertyKindString)},
				}}},
		},
		Parts: []handler.Part{
			{Key: "notes", Name: "Notes", Pos: "a0", UI: map[string]any{"type": "table"},
				Datasets: []handler.PartDataset{{Name: "testdoc_notes"}}},
			{Key: "log", Hidden: true, Uses: []string{"testdoc_notes"},
				Datasets: []handler.PartDataset{{Name: "testdoc_log"}}},
		},
	})
	extraCatalog.modules = append(extraCatalog.modules, handler.Module{
		Name: "reserved_notes", Canonical: "reserved_notes_shared",
		SharedOnly: true, Reserved: true, DataVersion: "reserved_notes-v1",
		New: func(handler.ModuleInstance) handler.Dataset {
			return handler.Dataset{Schema: handler.Schema{Dynamic: true}}
		},
	})
}

// TestServer_RegisteredTypeParts pins the read side of a registered
// type's static parts: the compiled view with the declared keys as
// ids, collections equal to the dataset names, `records` as the module
// of a schema-only dataset, the hidden part flagged; the type itself
// hidden from the default listing yet resolvable by id, and immutable.
func TestServer_RegisteredTypeParts(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "StaticParts")
	base := "/v1/spaces/" + sp.Id

	var parts api.TypePartsListResponse
	decodeGet(t, e, base+"/types/testdoc/parts", &parts)
	if len(parts.Parts) != 2 {
		t.Fatalf("parts = %+v, want two", parts.Parts)
	}
	// Parts come back by key, like a user type's: "log" before "notes".
	logPart, notes := parts.Parts[0], parts.Parts[1]
	if notes.Id != "notes" || notes.Key != "notes" || notes.Name != "Notes" || notes.Pos != "a0" || string(notes.UI) != `{"type":"table"}` {
		t.Errorf("notes part = %+v", notes)
	}
	if len(notes.Datasets) != 1 || notes.Datasets[0].Collection != "testdoc_notes" || notes.Datasets[0].Module != api.ModuleRecords ||
		notes.Datasets[0].PartId != "notes" || len(notes.Datasets[0].Fields) != 1 || notes.Datasets[0].Fields[0].Key != "title" {
		t.Errorf("notes datasets = %+v", notes.Datasets)
	}
	if !logPart.Hidden || len(logPart.Uses) != 1 || logPart.Uses[0] != "testdoc_notes" || len(logPart.Datasets) != 1 || logPart.Datasets[0].Collection != "testdoc_log" {
		t.Errorf("log part = %+v", logPart)
	}
	var defs api.TypeDatasetsListResponse
	decodeGet(t, e, base+"/types/testdoc/datasets", &defs)
	if len(defs.Datasets) != 2 {
		t.Errorf("flat datasets = %+v, want two", defs.Datasets)
	}

	// Hidden from the default listing, present with includeHidden,
	// resolvable by id, static.
	listIds := func(q string) map[string]api.TypeInfo {
		t.Helper()
		var list api.TypesListResponse
		decodeGet(t, e, base+"/types"+q, &list)
		out := map[string]api.TypeInfo{}
		for _, ti := range list.Types {
			out[ti.Id] = ti
		}
		return out
	}
	if _, listed := listIds("")["testdoc"]; listed {
		t.Error("hidden registered type listed by default")
	}
	if ti, ok := listIds("?includeHidden=true")["testdoc"]; !ok || !ti.Hidden || !ti.BuiltIn {
		t.Errorf("includeHidden row = %+v (ok=%v), want hidden built-in", ti, ok)
	}
	var info api.TypeInfo
	decodeGet(t, e, base+"/types/testdoc", &info)
	if !info.Hidden || info.XKey != "testdoc" {
		t.Errorf("get = %+v", info)
	}
	rec := doJSON(t, e, http.MethodPost, base+"/types/testdoc/parts", `{"key":"x","datasets":[{"module":"editor","shared":true}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("add part on a registered type: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "type.registered")

	// The write gate follows the static declaration: an object carrying
	// testdoc holds the collection, a bare one does not.
	obj := mustCreateObject(t, e, sp.Id, `{"types":["testdoc"]}`)
	write := func(objectId string) int {
		rec := doJSON(t, e, http.MethodPost, base+"/modify",
			`{"objectId":"`+objectId+`","dataset":"testdoc_notes","records":[{"upsert":true,"ops":[{"type":"$set","path":"title","value":"hello"}]}]}`)
		return rec.Code
	}
	if code := write(obj); code != http.StatusOK {
		t.Errorf("write into a static part dataset: %d", code)
	}
	bare := mustCreateObject(t, e, sp.Id, `{}`)
	if code := write(bare); code != http.StatusBadRequest {
		t.Errorf("write without the declaring type: %d, want 400", code)
	}
}

// TestServer_ReservedModule pins that a runtime declaration cannot name
// a reserved module — on a part, on a dataset added to a part, or in a
// bundle body — and that the refused bundle installs nothing.
func TestServer_ReservedModule(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, typeId, _ := setupSubscribeFixture(t, e)
	base := "/v1/spaces/" + spaceId
	rec := doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts",
		`{"key":"secret","datasets":[{"module":"reserved_notes","shared":true}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("part naming a reserved module: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, api.ErrDatasetModuleReserved)

	partId := mustAddPart(t, e, spaceId, typeId, `{"key":"body","datasets":[{"module":"editor","shared":true}]}`)
	rec = doJSON(t, e, http.MethodPost, base+"/types/"+typeId+"/parts/"+partId+"/datasets",
		`{"module":"reserved_notes","shared":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dataset naming a reserved module: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, api.ErrDatasetModuleReserved)

	rec = doJSON(t, e, http.MethodPost, base+"/bundles",
		`{"id":"secret/v1","derived":true,"parts":[{"key":"secret","datasets":[{"module":"reserved_notes","shared":true}]}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bundle naming a reserved module: %d %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, api.ErrDatasetModuleReserved)
	rec = doJSON(t, e, http.MethodGet, base+"/bundles/"+escapedBundleId("secret/v1"), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("refused bundle must not be installed: %d %s", rec.Code, rec.Body.String())
	}
}

// TestServer_BundleDeclaredType pins a bundle declaring a full type on
// its root: properties with handle-derived ids, layout and weight,
// listed (hidden is explicit); the definitions usable on a carrier;
// adopt declaring nothing twice; and the body's validation.
func TestServer_BundleDeclaredType(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	sp := createSpaceInfo(t, e, "BundleType")
	base := "/v1/spaces/" + sp.Id
	// `1.0` is a JSON number too: it must land as weight 1, not 0.
	body := `{"id":"wiki-test/v1","name":"Wiki","derived":true,"weight":1.0,"layout":{"type":"page"},"rootProperties":null,` +
		`"properties":[{"xKey":"parentId","name":"Parent","kind":"string"},` +
		`{"xKey":"pos","name":"Position","kind":"string","xFormat":{"type":"text"}}]}`
	first := ensureBundle(t, e, sp.Id, body)
	if !first.Installed || !first.Bundle.Derived || first.Bundle.RootId == "" {
		t.Fatalf("install: %+v (installed=%v)", first.Bundle, first.Installed)
	}
	root := first.Bundle.RootId

	var info api.TypeInfo
	decodeGet(t, e, base+"/types/"+root, &info)
	if info.Weight != 1 || string(info.Layout) != `{"type":"page"}` || info.Hidden {
		t.Errorf("root type = %+v, want weight 1, page layout, listed", info)
	}
	var list api.TypesListResponse
	decodeGet(t, e, base+"/types", &list)
	var listed bool
	for _, ti := range list.Types {
		listed = listed || ti.Id == root
	}
	if !listed {
		t.Error("a declared type must be listed by default")
	}
	var props api.PropertiesListResponse
	decodeGet(t, e, base+"/types/"+root+"/properties", &props)
	ids := map[string]string{}
	for _, p := range props.Properties {
		ids[p.XKey] = p.Id
	}
	if len(props.Properties) != 2 || ids["parentId"] == "" || ids["pos"] == "" {
		t.Fatalf("properties = %+v", props.Properties)
	}

	// Values on a carrier through the ordinary property route.
	obj := mustCreateObject(t, e, sp.Id, `{"types":["`+root+`"]}`)
	rec := doJSON(t, e, http.MethodPost, base+"/properties/"+obj+"/set/"+root, `{"patch":{"`+ids["parentId"]+`":"any://o/x"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set a bundle-declared property: %d %s", rec.Code, rec.Body.String())
	}

	// Adopt: same ids, same count, nothing installed.
	second := ensureBundle(t, e, sp.Id, body)
	if second.Installed || second.Bundle.RootId != root {
		t.Fatalf("re-ensure: %+v (installed=%v)", second.Bundle, second.Installed)
	}
	var again api.PropertiesListResponse
	decodeGet(t, e, base+"/types/"+root+"/properties", &again)
	if len(again.Properties) != 2 {
		t.Fatalf("adopt redeclared properties: %+v", again.Properties)
	}
	for _, p := range again.Properties {
		if ids[p.XKey] != p.Id {
			t.Errorf("property %s id changed: %s vs %s", p.XKey, ids[p.XKey], p.Id)
		}
	}

	// Body validation, all before any root is minted.
	for name, tc := range map[string]struct {
		body   string
		status int
		code   string
	}{
		"xKey required":    {`{"id":"v/v1","properties":[{"name":"x","kind":"string"}]}`, http.StatusBadRequest, "request.missing_field"},
		"xKey twice":       {`{"id":"v/v1","properties":[{"xKey":"a","kind":"string"},{"xKey":"a","kind":"number"}]}`, http.StatusBadRequest, "request.invalid_field"},
		"kind required":    {`{"id":"v/v1","properties":[{"xKey":"a"}]}`, http.StatusBadRequest, "request.schema"},
		"unknown key":      {`{"id":"v/v1","properties":[{"xKey":"a","kind":"string","format":"x"}]}`, http.StatusBadRequest, "request.unknown_field"},
		"layout shape":     {`{"id":"v/v1","layout":{"config":{}}}`, http.StatusBadRequest, "request.invalid_field"},
		"weight shape":     {`{"id":"v/v1","weight":"heavy"}`, http.StatusBadRequest, "request.schema"},
		"weight fraction":  {`{"id":"v/v1","weight":2.5,"properties":[{"xKey":"a","kind":"string"}]}`, http.StatusBadRequest, "request.schema"},
		"hidden shape":     {`{"id":"v/v1","hidden":"yes"}`, http.StatusBadRequest, "request.schema"},
		"metadata alone":   {`{"id":"v/v1","weight":2,"hidden":true,"layout":{"type":"page"}}`, http.StatusBadRequest, "request.invalid_field"},
		"created + types":  {`{"id":"v/v1","properties":[{"xKey":"a","kind":"string"}],"rootTypes":["x"]}`, http.StatusBadRequest, "type.not_found"},
		"too many":         {`{"id":"v/v1","properties":[` + repeatProps(65) + `]}`, http.StatusBadRequest, "request.invalid_field"},
		"reserved id":      {`{"id":"system:wiki/v1","derived":true,"weight":1,"properties":[{"xKey":"a","kind":"string"}]}`, http.StatusConflict, api.ErrBundleReserved},
		"reserved id bare": {`{"id":"system:x"}`, http.StatusConflict, api.ErrBundleReserved},
	} {
		rec := doJSON(t, e, http.MethodPost, base+"/bundles", tc.body)
		if rec.Code != tc.status {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, tc.code)
	}
	rec = doJSON(t, e, http.MethodGet, base+"/bundles/"+escapedBundleId("v/v1"), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a refused request must install nothing: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodGet, base+"/bundles/"+escapedBundleId("system:wiki/v1"), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a reserved id must install nothing: %d %s", rec.Code, rec.Body.String())
	}

	// The tech space takes a properties-only bundle, and its columns
	// evolve through the property routes on the root.
	var acc api.AccountResponse
	decodeGet(t, e, "/v1/account", &acc)
	techBase := "/v1/spaces/" + acc.TechSpaceId
	rec = doJSON(t, e, http.MethodPost, techBase+"/bundles", `{"id":"labels-test/v1","derived":true,"weight":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tech bundle with metadata alone: %d %s", rec.Code, rec.Body.String())
	}
	labels := ensureBundle(t, e, acc.TechSpaceId, `{"id":"labels-test/v1","name":"Labels","derived":true,"hidden":true,"properties":[{"xKey":"color","kind":"string"}]}`)
	if !labels.Installed || labels.Bundle.RootId == "" {
		t.Fatalf("tech properties-only install: %+v", labels)
	}
	var techProps api.PropertiesListResponse
	decodeGet(t, e, techBase+"/types/"+labels.Bundle.RootId+"/properties", &techProps)
	if len(techProps.Properties) != 1 || techProps.Properties[0].XKey != "color" {
		t.Fatalf("tech bundle properties = %+v", techProps.Properties)
	}
	rec = doJSON(t, e, http.MethodPatch, techBase+"/types/"+labels.Bundle.RootId+"/properties/"+techProps.Properties[0].Id, `{"set":{"name":"Colour"}}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("patch a tech bundle property: %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodPost, techBase+"/types/"+labels.Bundle.RootId+"/properties", `{"xKey":"icon","kind":"string"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add a property on a tech bundle root: %d %s", rec.Code, rec.Body.String())
	}
}

// repeatProps builds n distinct property drafts.
func repeatProps(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ","
		}
		b, _ := json.Marshal(map[string]any{"xKey": "k" + string(rune('a'+i%26)) + string(rune('a'+i/26)), "kind": "string"})
		out += string(b)
	}
	return out
}
