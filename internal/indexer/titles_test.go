package indexer

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

func TestTitleTiers(t *testing.T) {
	cases := []struct {
		typed string
		terms []string
		tiers []string
	}{
		{"Mario Ka", []string{"mario", "ka"}, []string{`^"mario ka"`, `^"mario ka"*`, `"mario ka"*`, `"mario" "ka"*`}},
		{"mario kart ", []string{"mario", "kart"}, []string{`^"mario kart"`, `^"mario kart"`, `"mario kart"`, `"mario" "kart"`}},
		{"zel", []string{"zel"}, []string{`^"zel"`, `^"zel"*`, `"zel"*`}},
		{"m", []string{"m"}, []string{`^"m"`, `^"m"*`, `"m"*`}},
		// Typed operators are text: the analyzer drops them.
		{`^"mario"* kart`, []string{"mario", "kart"}, []string{`^"mario kart"`, `^"mario kart"*`, `"mario kart"*`, `"mario" "kart"*`}},
		// A quote inside a word is doubled for the parser.
		{`צה"ל`, []string{`צה"ל`}, []string{`^"צה""ל"`, `^"צה""ל"*`, `"צה""ל"*`}},
		// CJK runs analyze to bigrams; the tiers carry them as a phrase.
		{"東京都", []string{"東京", "京都"}, []string{`^"東京 京都"`, `^"東京 京都"*`, `"東京 京都"*`, `"東京" "京都"*`}},
		{"   ", nil, nil},
		{"***", nil, nil},
	}
	kinds := []string{"exact", "prefix", "phrase", "words"}
	for _, c := range cases {
		terms, tiers := titleTiers(c.typed)
		var searches []string
		for i, tier := range tiers {
			searches = append(searches, tier.search)
			if tier.match != kinds[i] {
				t.Errorf("%q: tier %d is %s, want %s", c.typed, i, tier.match, kinds[i])
			}
		}
		if !slices.Equal(terms, c.terms) || !slices.Equal(searches, c.tiers) {
			t.Errorf("%q: terms %q tiers %q, want %q %q", c.typed, terms, searches, c.terms, c.tiers)
		}
	}
}

// titleCorpus is the game-console shape of the research: names that
// share words, plus body windows that repeat them.
func titleCorpus(t *testing.T, st *Store, sp string, vec []float32) {
	t.Helper()
	ups := []DocUpsert{
		{Entry: nameEntry("mk", "Mario Kart", 1)},
		{Entry: nameEntry("mk64", "Mario Kart 64", 2)},
		{Entry: nameEntry("mk8", "Mario Kart 8", 3)},
		{Entry: nameEntry("smk", "Super Mario Kart", 4)},
		{Entry: nameEntry("kme", "Kart: Mario Edition", 5)},
		{Entry: nameEntry("kanz", "Kanzenban Mario", 6)},
		{Entry: nameEntry("zelda", "The Legend of Zelda", 7)},
		{Entry: nameEntry("zelda2", "Zelda II: The Adventure of Link", 8)},
		{Entry: nameEntry("mp", "Mario Paint", 9)},
		{Entry: nameEntry("tokyo", "東京都", 10)},
		// Body windows: the words repeat, so BM25 alone would rank them
		// above the short name docs' competitors.
		{Entry: entry(index.ScopeBasic, "mk", "editor_blocks", "win_1", strings.Repeat("mario kart release years; mario kart tables. ", 6), 11)},
		{Entry: entry(index.ScopeBasic, "mk8", "editor_blocks", "win_1", "mario kart awards and the kart kart kart", 12)},
		{Entry: entry("chat", "chat", "chat_messages", "m1", "who wants to play mario kart tonight", 13)},
	}
	if vec != nil {
		for i := range ups {
			ups[i].Vector = vec
		}
	}
	if err := st.Apply(context.Background(), sp, ups, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func objectIds(hits []api.SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ObjectId
	}
	return out
}

func matches(hits []api.SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Match
	}
	return out
}

// Title mode: the tiers in order, exact names first, one hit per
// object, the last word a prefix unless a space follows it.
func TestIndexer_TitleMode(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	ix := &Indexer{store: st, opts: Options{AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"
	titleCorpus(t, st, sp, nil)

	search := func(q string, limit int) api.SearchResponse {
		t.Helper()
		res, err := ix.Search(ctx, sp, api.SearchRequest{Query: q, Mode: api.SearchModeTitle, Limit: limit}, nil)
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		if res.Mode != api.SearchModeTitle || res.VectorStatus != api.VectorStatusDisabled {
			t.Fatalf("%q: mode %s vector %s", q, res.Mode, res.VectorStatus)
		}
		return res
	}

	res := search("mario ka", 10)
	// Starts-with first (the shortest name first within a tier), then
	// the phrase inside a name, then the bag: "Kanzenban Mario" holds
	// "mario" and a "ka"-word, so the bag tier admits it, and it is
	// shorter than "Kart: Mario Edition".
	want := []string{"mk", "mk64", "mk8", "smk", "kanz", "kme"}
	if got := objectIds(res.Hits); !slices.Equal(got, want) {
		t.Fatalf("mario ka: %v, want %v (%v)", got, want, matches(res.Hits))
	}
	if got := matches(res.Hits); !slices.Equal(got, []string{"prefix", "prefix", "prefix", "phrase", "words", "words"}) {
		t.Fatalf("mario ka matches: %v", got)
	}
	for _, h := range res.Hits {
		if h.Dataset != index.DatasetProp || h.RecordId != index.NamePropRecordId || h.Scope != index.ScopeBasic || h.Passages != nil {
			t.Fatalf("title hit shape: %+v", h)
		}
	}
	if res.Hits[0].Data != "Mario Kart" || res.Hits[0].DataTotal != len("Mario Kart") {
		t.Fatalf("title hit data: %+v", res.Hits[0])
	}

	// The typed title itself is exact and leads; "Mario Kart 64" and
	// "Mario Kart 8" are prefixes of it.
	res = search("mario kart", 10)
	if got := matches(res.Hits); !slices.Equal(got[:3], []string{"exact", "prefix", "prefix"}) || res.Hits[0].ObjectId != "mk" {
		t.Fatalf("mario kart: %v %v", objectIds(res.Hits), got)
	}
	// A trailing space closes the last word: "Mario Kart" is still
	// exact, "Mario Karting" would no longer match.
	res = search("mario kart ", 10)
	if res.Hits[0].Match != "exact" || res.Hits[0].ObjectId != "mk" {
		t.Fatalf("mario kart (space): %v %v", objectIds(res.Hits), matches(res.Hits))
	}
	// Any order: the bag tier — after the one name that starts with it.
	res = search("kart mar", 10)
	if got := matches(res.Hits); !slices.Equal(got, []string{"prefix", "words", "words", "words", "words"}) || res.Hits[0].ObjectId != "kme" {
		t.Fatalf("kart mar: %v %v", objectIds(res.Hits), got)
	}
	// One letter: every name holding an m-word, the ones starting with it first.
	res = search("m", 10)
	if got := matches(res.Hits); len(res.Hits) != 7 || got[0] != "prefix" || got[len(got)-1] != "phrase" {
		t.Fatalf("m: %v %v", objectIds(res.Hits), got)
	}
	// A stop word is a title word.
	res = search("the leg", 10)
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"zelda"}) || res.Hits[0].Match != "prefix" {
		t.Fatalf("the leg: %v %v", got, matches(res.Hits))
	}
	// CJK: the typed run is a bigram phrase.
	res = search("東京", 10)
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"tokyo"}) || res.Hits[0].Match != "prefix" {
		t.Fatalf("東京: %v %v", got, matches(res.Hits))
	}
	// Nothing to match.
	res = search("***", 10)
	if len(res.Hits) != 0 {
		t.Fatalf("operators only: %v", objectIds(res.Hits))
	}
	// limit cuts after the tiers that fill it; later tiers never run.
	res = search("mario ka", 2)
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"mk", "mk64"}) {
		t.Fatalf("limit 2: %v", got)
	}
	// require / exclude bind the title hits, scoped to the name.
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "mario", Mode: api.SearchModeTitle, Exclude: []string{"kart"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"mp", "kanz"}) {
		t.Fatalf("mario -kart: %v %v", got, matches(res.Hits))
	}
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "mario", Mode: api.SearchModeTitle, Require: []string{"super"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"smk"}) {
		t.Fatalf("mario +super: %v", got)
	}
	// The host filter binds too, and limit counts matching objects.
	hf := newSetFilter([]string{"mk", "mk64", "mk8", "smk", "kme", "kanz", "zelda", "zelda2", "mp", "tokyo", "chat"}, "mk8", "smk")
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "mario ka", Mode: api.SearchModeTitle, Limit: 1}, hf)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"mk8"}) || res.Truncated {
		t.Fatalf("filtered: %v truncated=%v", got, res.Truncated)
	}
}

// The exact name leads whatever the page size: its own tier finds it
// before the prefix tier's cut, where same-length completions tie on
// score and fall in insertion order; the page's ties list alphabetically.
func TestIndexer_TitleExactLeadsSmallPage(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	ix := &Indexer{store: st, opts: Options{AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"
	if err := st.Apply(ctx, sp, []DocUpsert{
		{Entry: nameEntry("notes", "Notes", 1)},
		{Entry: nameEntry("notebook", "Notebook", 2)},
		{Entry: nameEntry("noted", "Noted", 3)},
		{Entry: nameEntry("note", "Note", 4)},
	}, nil, nil); err != nil {
		t.Fatal(err)
	}
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "note", Mode: api.SearchModeTitle, Limit: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"note", "notebook", "noted"}) || res.Hits[0].Match != "exact" || res.Hits[1].Match != "prefix" {
		t.Fatalf("note, limit 3: %v %v", got, matches(res.Hits))
	}
	// Ties in the tier: alphabetical, not insertion order.
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "note", Mode: api.SearchModeTitle, Limit: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"note", "notebook", "noted", "notes"}) {
		t.Fatalf("note, limit 10: %v %v", got, matches(res.Hits))
	}
}

// A name past the chunk bound is one title hit — its head — that never
// anchors on a later chunk; its later chunks are content, and the
// content legs do not list the record a second time.
func TestIndexer_TitleChunkedName(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	ix := &Indexer{store: st, opts: Options{AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"
	long := "Lorem ipsum " + strings.Repeat("dolor sit amet ", DefaultChunkRunes/8) + "zzfinal"
	ups := expandEntry(nameEntry("long", long, 1), DefaultChunkRunes)
	if len(ups) < 2 {
		t.Fatalf("chunks = %d, want several", len(ups))
	}
	if err := st.Apply(ctx, sp, ups, nil, nil); err != nil {
		t.Fatal(err)
	}
	title := func(q string) api.SearchResponse {
		t.Helper()
		res, err := ix.Search(ctx, sp, api.SearchRequest{Query: q, Mode: api.SearchModeTitle}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := title("lorem"); len(res.Hits) != 1 || res.Hits[0].Match != "prefix" || res.Hits[0].Chunk != 0 {
		t.Fatalf("lorem: %+v", res.Hits)
	}
	if res := title("ipsum"); len(res.Hits) != 1 || res.Hits[0].Match != "phrase" {
		t.Fatalf("ipsum: %+v", res.Hits)
	}
	// The tail is not the name: no title hit, a content hit.
	if res := title("zzfinal"); len(res.Hits) != 0 {
		t.Fatalf("zzfinal title: %+v", res.Hits)
	}
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "zzfinal", Mode: api.SearchModeFTS}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Match != "" || res.Hits[0].Chunk == 0 {
		t.Fatalf("zzfinal fts: %+v", res.Hits)
	}
	// Both the head and a later chunk match: one hit, the title.
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "dolor", Mode: api.SearchModeFTS}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Match != "phrase" {
		t.Fatalf("dolor fts: %+v", res.Hits)
	}
}

// The content legs skip the records shown as titles before counting
// them towards their cover, so a page stays full when titles take most
// of it and one dense record dominates the lexical window.
func TestIndexer_TitlesDoNotStarveContent(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	ix := &Indexer{store: st, opts: Options{AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"
	ups := []DocUpsert{
		{Entry: nameEntry("a1", "Alpha one", 1)},
		{Entry: nameEntry("a2", "Alpha two", 2)},
		{Entry: nameEntry("a3", "Alpha three", 3)},
	}
	dense := expandEntry(entry("basic", "A", "editor_blocks", "r", strings.Repeat("alpha alpha alpha. ", 40*DefaultChunkRunes/19), 4), DefaultChunkRunes)
	if len(dense) < 40 {
		t.Fatalf("dense chunks = %d, want >= 40", len(dense))
	}
	ups = append(ups, dense...)
	ups = append(ups, DocUpsert{Entry: entry("chat", "B", "chat_messages", "r", "one alpha mention among many other words about other things entirely", 5)})
	if err := st.Apply(ctx, sp, ups, nil, nil); err != nil {
		t.Fatal(err)
	}
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "alpha", Mode: api.SearchModeFTS, Limit: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"a1", "a3", "a2", "A", "B"}) || res.Truncated {
		t.Fatalf("alpha, limit 5: %v %v truncated=%v", got, matches(res.Hits), res.Truncated)
	}
}

// require / exclude bind a title hit against the name: a term that
// occurs only in a body empties the title block while the body still
// answers through the content legs.
func TestIndexer_TitleRequireScopedToName(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 0)
	ix := &Indexer{store: st, opts: Options{AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"
	titleCorpus(t, st, sp, nil)
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "mario kart", Mode: api.SearchModeFTS, Require: []string{"tables"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].ObjectId != "mk" || res.Hits[0].RecordId != "win_1" || res.Hits[0].Match != "" {
		t.Fatalf("require tables: %+v", res.Hits)
	}
}

// queryCountingEmbedder counts the query embeddings asked of it.
type queryCountingEmbedder struct {
	axisEmbedder
	queries int
}

func (c *queryCountingEmbedder) EmbedQuery(ctx context.Context, q string) ([]float32, error) {
	c.queries++
	return c.axisEmbedder.EmbedQuery(ctx, q)
}

// A page the titles fill embeds no query.
func TestIndexer_TitlesFullPageSkipsEmbedding(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	emb := &queryCountingEmbedder{axisEmbedder: axisEmbedder{dim: 4}}
	ix := &Indexer{store: st, opts: Options{Embedder: emb, AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"
	titleCorpus(t, st, sp, []float32{1, 0, 0, 0})
	if _, err := ix.Search(ctx, sp, api.SearchRequest{Query: "mario kart", Limit: 3}, nil); err != nil {
		t.Fatal(err)
	}
	if emb.queries != 0 {
		t.Fatalf("full page embedded the query %d times", emb.queries)
	}
	if _, err := ix.Search(ctx, sp, api.SearchRequest{Query: "mario kart", Limit: 10}, nil); err != nil {
		t.Fatal(err)
	}
	if emb.queries != 1 {
		t.Fatalf("free page embedded the query %d times, want 1", emb.queries)
	}
}

// Hybrid and fts lead with the title block; the content legs fill the
// rest, never repeating a name doc, still listing the body windows of a
// title-listed object; a page the titles fill runs no leg and embeds no
// query; a search scoped away from basic runs no title pass; vector
// mode stays pure.
func TestIndexer_SearchTitlesFirst(t *testing.T) {
	ctx := context.Background()
	st := mustStore(t, 4)
	emb := axisEmbedder{dim: 4}
	ix := &Indexer{store: st, opts: Options{Embedder: emb, AnnounceAfter: -1}.withDefaults()}
	const sp = "sp1"
	titleCorpus(t, st, sp, []float32{1, 0, 0, 0})
	if ok, err := st.EnsureVectorIndex(ctx, sp); err != nil || !ok {
		t.Fatalf("ensure vector index: %v (ok=%v)", err, ok)
	}

	for _, mode := range []string{api.SearchModeHybrid, api.SearchModeFTS} {
		res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "mario kart", Mode: mode, Limit: 10}, nil)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if res.Mode != mode {
			t.Fatalf("%s ran as %s", mode, res.Mode)
		}
		got := objectIds(res.Hits)
		// "kart*" completes no word of "Kanzenban Mario", so it is no
		// title hit for this query.
		titles := []string{"mk", "mk64", "mk8", "smk", "kme"}
		if len(got) < 5 || !slices.Equal(got[:5], titles) || !slices.Equal(matches(res.Hits)[:5], []string{"exact", "prefix", "prefix", "phrase", "words"}) {
			t.Fatalf("%s: titles do not lead: %v %v", mode, got, matches(res.Hits))
		}
		// The content block fills the rest of the page: the windows of
		// title-listed objects (the unit is a record), the chat message,
		// and the name docs the title pass did not admit — never a name
		// doc already shown.
		if len(res.Hits) != 10 {
			t.Fatalf("%s: hits = %d (%v), want 5 titles + 5 content records", mode, len(res.Hits), got)
		}
		windows := 0
		for _, h := range res.Hits[5:] {
			if h.Match != "" {
				t.Fatalf("%s: content block holds a title hit: %+v", mode, h)
			}
			if h.RecordId == index.NamePropRecordId && slices.Contains(titles, h.ObjectId) {
				t.Fatalf("%s: a shown name doc came back through the content leg: %+v", mode, h)
			}
			if h.RecordId == "win_1" {
				windows++
			}
		}
		if windows != 2 {
			t.Fatalf("%s: body windows of title-listed objects: %d, want 2: %v", mode, windows, got[5:])
		}
		if mode == api.SearchModeHybrid && res.VectorStatus != api.VectorStatusUsed {
			t.Fatalf("hybrid with a free page: vectorStatus %s", res.VectorStatus)
		}
	}

	// Titles fill the page: no content leg, no embedding.
	res, err := ix.Search(ctx, sp, api.SearchRequest{Query: "mario kart", Mode: api.SearchModeHybrid, Limit: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"mk", "mk64", "mk8"}) || res.VectorStatus != api.VectorStatusSkipped || res.Mode != api.SearchModeHybrid {
		t.Fatalf("full page: %v vector %s mode %s", got, res.VectorStatus, res.Mode)
	}

	// Scoped away from basic: no title pass, the chat hit alone.
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "mario kart", Mode: api.SearchModeFTS, Scopes: []string{"chat"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := objectIds(res.Hits); !slices.Equal(got, []string{"chat"}) || res.Hits[0].Match != "" {
		t.Fatalf("chat scope: %v %v", got, matches(res.Hits))
	}
	// A scope set that includes basic runs it.
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "mario kart", Mode: api.SearchModeFTS, Scopes: []string{"chat", "basic"}, Limit: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := matches(res.Hits); !slices.Equal(got, []string{"exact", "prefix"}) {
		t.Fatalf("chat+basic scope: %v %v", objectIds(res.Hits), got)
	}

	// Vector mode has no title block.
	res, err = ix.Search(ctx, sp, api.SearchRequest{Query: "mario kart", Mode: api.SearchModeVector, Limit: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Hits {
		if h.Match != "" {
			t.Fatalf("vector mode title hit: %+v", h)
		}
	}
}
