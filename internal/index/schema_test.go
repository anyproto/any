package index

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"
)

func schemaDoc(t *testing.T, search map[string]string) json.RawMessage {
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
		{Name: "objects", JSONSchema: schemaDoc(t, nil)},                                                             // built-in: TypeId ""
		{Name: "chat_messages", TypeId: "chat", JSONSchema: schemaDoc(t, map[string]string{"text": "text"})},         // static: skipped
		{Name: "articles", TypeId: "t1", JSONSchema: schemaDoc(t, map[string]string{"title": "title", "text": "body"})},
		{Name: "notes", TypeId: "t1", JSONSchema: schemaDoc(t, map[string]string{"text": "content"})},                // text-only
		{Name: "headlines", TypeId: "t2", JSONSchema: schemaDoc(t, map[string]string{"title": "headline"})},          // title-only
		{Name: "plain", TypeId: "t2", JSONSchema: schemaDoc(t, nil)},                                                 // no x-search: tracked, not searchable
		{Name: "broken", TypeId: "t2", JSONSchema: json.RawMessage(`{"x-search": 42}`)},                              // malformed: tracked, not searchable
		{Name: "odd:name", TypeId: "t2", JSONSchema: schemaDoc(t, map[string]string{"text": "x"})},                   // colon: ignored entirely
	}
	searchable, allNames := parseSchemaDatasets(list, skip)

	want := []schemaDataset{
		{name: "articles", typeId: "t1", titleField: "title", textField: "body"},
		{name: "notes", typeId: "t1", titleField: "", textField: "content"},
		{name: "headlines", typeId: "t2", titleField: "headline", textField: ""},
	}
	if !reflect.DeepEqual(searchable, want) {
		t.Errorf("searchable = %+v, want %+v", searchable, want)
	}
	sort.Strings(allNames)
	wantNames := []string{"articles", "broken", "headlines", "notes", "plain"}
	if !reflect.DeepEqual(allNames, wantNames) {
		t.Errorf("allNames = %v, want %v", allNames, wantNames)
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
