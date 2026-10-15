package indexer

import (
	"context"
	"slices"
	"strings"
	"testing"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any/internal/index"
)

// nameEntry is the prop chunker's name entry for an object.
func nameEntry(objectId, name string, seq uint64) index.IndexEntry {
	return entry(index.ScopeBasic, objectId, index.DatasetProp, index.NamePropRecordId, name, seq)
}

func ftsFields(t *testing.T, st *Store, sp string) []string {
	t.Helper()
	coll, err := st.spaceColl(context.Background(), sp)
	if err != nil {
		t.Fatal(err)
	}
	for _, ix := range coll.GetIndexes() {
		if info := ix.Info(); info.Name == ftsIndex {
			return info.Fields
		}
	}
	t.Fatal("no fts index")
	return nil
}

// A fresh collection indexes name first, then data; the name doc's
// text lives in `name`, is read back as the hit's Data, is the embed
// text of a pending name, and is reachable both scoped and unscoped.
func TestStore_NameField(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	const sp = "sp1"
	if got := ftsFields(t, st, sp); !slices.Equal(got, []string{nameField, "data"}) {
		t.Fatalf("fts fields: %v", got)
	}
	long := strings.Repeat("Mario Kart ", 8) // past minPropEmbedBytes: embeds
	if err := st.Apply(ctx, sp, []DocUpsert{
		{Entry: nameEntry("mk", "Mario Kart", 1)},
		{Entry: nameEntry("long", long, 2)},
		{Entry: entry("basic", "mk", "editor_blocks", "win_1", "mario kart release years", 3)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	coll, err := st.spaceColl(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := coll.FindId(ctx, docId("mk", index.DatasetProp, index.NamePropRecordId))
	if err != nil {
		t.Fatal(err)
	}
	if v := doc.Value(); string(v.GetStringBytes(nameField)) != "Mario Kart" || (v.Get("data") != nil) {
		t.Fatalf("name doc: %s", v.String())
	}

	scoped, err := st.SearchFTSQuery(ctx, sp, FTSQuery{Query: `^"mario ka"*`, Fields: []string{nameField}}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 2 || scoped[0].RecordId != index.NamePropRecordId || scoped[0].Data != "Mario Kart" {
		t.Fatalf("scoped: %+v", scoped)
	}
	all, err := st.SearchFTS(ctx, sp, "mario", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unscoped: %+v", all)
	}
	ids, texts, err := st.Pending(ctx, sp, 10)
	if err != nil {
		t.Fatal(err)
	}
	if i := slices.Index(ids, docId("long", index.DatasetProp, index.NamePropRecordId)); i < 0 || texts[i] != long {
		t.Fatalf("pending: %v %q", ids, texts)
	}
	// An unknown scope field is the engine's error, not a silent miss.
	if _, err := st.SearchFTSQuery(ctx, sp, FTSQuery{Query: "mario", Fields: []string{"nope"}}, nil, 0); err == nil {
		t.Fatal("unknown $fields: no error")
	}
}

// A collection written before the name field — the index over `data`
// alone (or `data` + `title`), the name in `data` — is moved to the
// current layout on its first open: the index is recreated over the
// new field set and the name docs' text moves to `name`, while every
// other doc, its hash and its vector stay as they were.
func TestStore_UpgradeFTSToNameField(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	const sp = "sp1"
	coll, err := st.db.Collection(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if err := coll.EnsureIndex(ctx,
		anystore.IndexInfo{Name: objectIdIndex, Fields: []string{"objectId"}},
		anystore.IndexInfo{Name: ftsIndex, Kind: anystore.IndexKindFulltext, Fields: []string{"data", "title"}},
	); err != nil {
		t.Fatal(err)
	}
	arena := &anyenc.Arena{}
	old := func(objectId, dataset, recordId, data string, vec bool) *anyenc.Value {
		d := arena.NewObject()
		d.Set("id", arena.NewString(docId(objectId, dataset, recordId)))
		d.Set("scope", arena.NewString("basic"))
		d.Set("objectId", arena.NewString(objectId))
		d.Set("dataset", arena.NewString(dataset))
		d.Set("recordId", arena.NewString(recordId))
		d.Set("data", arena.NewString(data))
		d.Set("title", arena.NewString(""))
		d.Set("hash", arena.NewString(docHash(data, "")))
		d.Set("applySeq", arena.NewNumberInt(1))
		if vec {
			d.Set("vector", arena.NewVectorF32([]float32{1, 0, 0, 0}))
		}
		return d
	}
	if err := coll.Insert(ctx,
		old("mk", index.DatasetProp, index.NamePropRecordId, "Mario Kart", false),
		old("mk", index.DatasetProp, index.DescriptionPropRecordId, "a racing game", false),
		old("mk", "editor_blocks", "win_1", "mario kart release years", true),
		// A valueless name doc is never written, but a legacy empty one
		// must not become a `name` field either.
		old("empty", index.DatasetProp, index.NamePropRecordId, "", false),
	); err != nil {
		t.Fatal(err)
	}

	if got := ftsFields(t, st, sp); !slices.Equal(got, []string{nameField, "data"}) {
		t.Fatalf("fts fields after upgrade: %v", got)
	}
	coll, err = st.spaceColl(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	get := func(id string) *anyenc.Value {
		t.Helper()
		doc, err := coll.FindId(ctx, id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return doc.Value()
	}
	if v := get(docId("mk", index.DatasetProp, index.NamePropRecordId)); string(v.GetStringBytes(nameField)) != "Mario Kart" || (v.Get("data") != nil) || string(v.GetStringBytes("hash")) != docHash("Mario Kart", "") {
		t.Fatalf("name doc after upgrade: %s", v.String())
	}
	if v := get(docId("mk", index.DatasetProp, index.DescriptionPropRecordId)); string(v.GetStringBytes("data")) != "a racing game" || (v.Get(nameField) != nil) {
		t.Fatalf("description doc touched: %s", v.String())
	}
	if v := get(docId("mk", "editor_blocks", "win_1")); !(v.Get("vector") != nil) || string(v.GetStringBytes("data")) != "mario kart release years" {
		t.Fatalf("window doc touched: %s", v.String())
	}
	if v := get(docId("empty", index.DatasetProp, index.NamePropRecordId)); v.Get(nameField) != nil {
		t.Fatalf("empty legacy name doc: %s", v.String())
	}
	scoped, err := st.SearchFTSQuery(ctx, sp, FTSQuery{Query: `^"mario"*`, Fields: []string{nameField}}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].ObjectId != "mk" || scoped[0].Data != "Mario Kart" {
		t.Fatalf("scoped after upgrade: %+v", scoped)
	}
	all, err := st.SearchFTS(ctx, sp, "mario", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("unscoped after upgrade: %+v", all)
	}
	// The hash still matches what the chunker would compute, so the
	// next stream of the object re-embeds nothing.
	hashes, err := st.DocHashes(ctx, sp, "mk:"+index.DatasetProp+":")
	if err != nil {
		t.Fatal(err)
	}
	if hashes[docId("mk", index.DatasetProp, index.NamePropRecordId)] != docHash("Mario Kart", "") {
		t.Fatalf("hashes: %v", hashes)
	}
}
