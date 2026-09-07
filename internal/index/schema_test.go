package index

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"
)

func schemaDoc(t *testing.T, search map[string]any) json.RawMessage {
	t.Helper()
	doc := map[string]any{"type": "object"}
	if search != nil {
		doc["x-search"] = search
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSchemaDatasets(t *testing.T) {
	skip := map[string]bool{"chat_messages": true, DatasetProp: true, DatasetSchemaVirtual: true}
	list := []space.DatasetSchema{
		{Name: "objects", JSONSchema: schemaDoc(t, nil)},                                                            // built-in: no owners
		{Name: "chat_messages", Owners: []string{"chat"}, JSONSchema: schemaDoc(t, map[string]any{"text": "text"})}, // static: skipped
		{Name: "articles", Owners: []string{"t1"}, JSONSchema: schemaDoc(t, map[string]any{"title": "title", "text": "body"})},
		{Name: "notes", Owners: []string{"t1"}, JSONSchema: schemaDoc(t, map[string]any{"text": "content"})},                                      // text-only
		{Name: "headlines", Owners: []string{"t2"}, JSONSchema: schemaDoc(t, map[string]any{"title": "headline"})},                                // title-only
		{Name: "plain", Owners: []string{"t2"}, JSONSchema: schemaDoc(t, nil)},                                                                    // no x-search: tracked, not searchable
		{Name: "broken", Owners: []string{"t2"}, JSONSchema: json.RawMessage(`{"x-search": 42}`)},                                                 // malformed: tracked, not searchable
		{Name: "odd:name", Owners: []string{"t2"}, JSONSchema: schemaDoc(t, map[string]any{"text": "x"})},                                         // colon: ignored entirely
		{Name: "scoped", Owners: []string{"t3"}, JSONSchema: schemaDoc(t, map[string]any{"text": "x", "scope": "recipes"})},                       // declared scope
		{Name: "badscope", Owners: []string{"t3"}, JSONSchema: schemaDoc(t, map[string]any{"text": "x", "scope": "Not A Slug"})},                  // invalid scope: tracked, not searchable
		{Name: "emails", Owners: []string{"t4"}, JSONSchema: schemaDoc(t, map[string]any{"title": "subject", "text": []string{"body", "notes"}})}, // multi-field text
		{Name: "single", Owners: []string{"t4"}, JSONSchema: schemaDoc(t, map[string]any{"text": []string{"body"}})},                              // one-element array
		{Name: "badtext", Owners: []string{"t4"}, JSONSchema: schemaDoc(t, map[string]any{"text": 42})},                                           // wrong text type: tracked, not searchable
	}
	searchable, unsearchable := parseSchemaDatasets(list, skip)

	want := []schemaDataset{
		{name: "articles", typeId: "t1", titleField: "title", textFields: []string{"body"}, scope: ScopeBasic, searchable: true},
		{name: "notes", typeId: "t1", titleField: "", textFields: []string{"content"}, scope: ScopeBasic, searchable: true},
		{name: "headlines", typeId: "t2", titleField: "headline", textFields: nil, scope: ScopeBasic, searchable: true},
		{name: "scoped", typeId: "t3", titleField: "", textFields: []string{"x"}, scope: "recipes", searchable: true},
		{name: "emails", typeId: "t4", titleField: "subject", textFields: []string{"body", "notes"}, scope: ScopeBasic, searchable: true},
		{name: "single", typeId: "t4", titleField: "", textFields: []string{"body"}, scope: ScopeBasic, searchable: true},
	}
	if !reflect.DeepEqual(searchable, want) {
		t.Errorf("searchable = %+v, want %+v", searchable, want)
	}
	// No-x-search, malformed and invalid-scope docs land in the
	// always-evicted set.
	sort.Strings(unsearchable)
	wantNames := []string{"badscope", "badtext", "broken", "plain"}
	if !reflect.DeepEqual(unsearchable, wantNames) {
		t.Errorf("unsearchable = %v, want %v", unsearchable, wantNames)
	}
}

func TestSchemaChunker_TrackRetired(t *testing.T) {
	c := NewSchemaChunker("chat_messages")

	c.trackRetired("sp1", []string{"articles", "notes"})
	c.trackRetired("sp2", []string{"articles"}) // per-space isolation

	if len(c.retired["sp1"]) != 0 {
		t.Fatalf("nothing retired yet, got %v", c.retired["sp1"])
	}

	// notes vanishes from sp1's catalog.
	c.trackRetired("sp1", []string{"articles"})
	if !c.retired["sp1"]["notes"] {
		t.Error("notes should be retired in sp1")
	}
	if len(c.retired["sp2"]) != 0 {
		t.Errorf("sp2 must be unaffected, got %v", c.retired["sp2"])
	}

	// Re-adding the name un-retires it.
	c.trackRetired("sp1", []string{"articles", "notes"})
	if c.retired["sp1"]["notes"] {
		t.Error("re-added notes should not stay retired")
	}
}

func TestRenderSearchValue(t *testing.T) {
	a := &anyenc.Arena{}
	obj := a.NewObject()
	obj.Set("s", a.NewString("hello"))
	obj.Set("n", a.NewNumberFloat64(42))
	arr := a.NewArray()
	arr.SetArrayItem(0, a.NewString("x"))
	arr.SetArrayItem(1, a.NewNumberFloat64(7))
	arr.SetArrayItem(2, a.NewTrue()) // skipped
	obj.Set("a", arr)
	obj.Set("b", a.NewTrue())
	obj.Set("o", a.NewObject())

	cases := []struct{ field, want string }{
		{"s", "hello"},
		{"n", "42"},
		{"a", "x\n7"},
		{"b", ""},
		{"o", ""},
		{"missing", ""},
	}
	for _, tc := range cases {
		if got := renderSearchValue(obj.Get(tc.field)); got != tc.want {
			t.Errorf("renderSearchValue(%s) = %q, want %q", tc.field, got, tc.want)
		}
	}
	if got := renderSearchValue(nil); got != "" {
		t.Errorf("renderSearchValue(nil) = %q, want empty", got)
	}
}

func TestParseSearchTextFields(t *testing.T) {
	cases := []struct {
		raw    string
		want   []string
		wantOk bool
	}{
		{"", nil, true},                      // absent
		{`""`, nil, true},                    // empty string = no mapping
		{`"body"`, []string{"body"}, true},   // bare string
		{`["body"]`, []string{"body"}, true}, // one-element array
		{`["body","notes"]`, []string{"body", "notes"}, true},
		{`["body","","notes"]`, []string{"body", "notes"}, true}, // empty keys dropped defensively
		{`[]`, nil, true},                                        // empty array = no mapping
		{`42`, nil, false},                                       // wrong type
		{`["body",42]`, nil, false},                              // non-string element
		{`{"k":"v"}`, nil, false},                                // wrong type
	}
	for _, tc := range cases {
		got, ok := parseSearchTextFields(json.RawMessage(tc.raw))
		if ok != tc.wantOk || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parseSearchTextFields(%s) = (%v, %v), want (%v, %v)", tc.raw, got, ok, tc.want, tc.wantOk)
		}
	}
}

func TestRenderTextFields(t *testing.T) {
	a := &anyenc.Arena{}
	rec := a.NewObject()
	rec.Set("body", a.NewString("first part"))
	rec.Set("notes", a.NewString("second part"))
	rec.Set("empty", a.NewString(""))

	cases := []struct {
		fields []string
		want   string
	}{
		{nil, ""},
		{[]string{"body"}, "first part"},
		{[]string{"body", "notes"}, "first part\n\nsecond part"}, // mapping order
		{[]string{"notes", "body"}, "second part\n\nfirst part"},
		{[]string{"body", "missing", "empty", "notes"}, "first part\n\nsecond part"}, // absent/empty skipped
		{[]string{"missing"}, ""},
	}
	for _, tc := range cases {
		if got := renderTextFields(rec, tc.fields); got != tc.want {
			t.Errorf("renderTextFields(%v) = %q, want %q", tc.fields, got, tc.want)
		}
	}
}

func TestJoinTitleText(t *testing.T) {
	cases := []struct{ title, text, want string }{
		{"", "", ""},
		{"T", "", "T"},
		{"", "B", "B"},
		{"T", "B", "T\nB"},
	}
	for _, tc := range cases {
		if got := joinTitleText(tc.title, tc.text); got != tc.want {
			t.Errorf("joinTitleText(%q, %q) = %q, want %q", tc.title, tc.text, got, tc.want)
		}
	}
}

func TestNewSchemaChunker_SkipSet(t *testing.T) {
	c := NewSchemaChunker("editor_blocks")
	for _, name := range []string{"editor_blocks", DatasetProp, DatasetSchemaVirtual} {
		if !c.skip[name] {
			t.Errorf("skip set must contain %q", name)
		}
	}
	if c.Dataset() != DatasetSchemaVirtual {
		t.Errorf("Dataset() = %q", c.Dataset())
	}
	if c.TypeId() != "" {
		t.Errorf("TypeId() = %q, want ungated", c.TypeId())
	}
}
