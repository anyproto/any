package server

import (
	"strings"
	"testing"

	"github.com/valyala/fastjson"

	"github.com/anyproto/any-store/v2/anyenc"
)

// benchRecordJSON is a representative objects row: one ~200-char
// string property, the built-in `any` / `nav` groups, the derived
// stamps, and a fully-enumerated `_ver`. ~1 KB on the wire.
const benchRecordJSON = `{
  "id": "bafyreiahq2n522avjpw7xka2lzzjrynpjttv4ddfrpxwj26rfjzpabtrii",
  "_ver": {
    "id": "!!$5",
    "any": {"types": "!!$5", "name": "!!$5"},
    "nav": {"pos": "!!$5", "type": "!!$5", "parentId": "!!$5"},
    "bafyreibjoqwn23nzx63cx7n7bkunvca4jueqbuhbz27rxvjbta5fxhvyoa": {"3zsJKegeZJu": "!!%>"},
    "author": "!!$5",
    "createdAt": "!!$5",
    "spaceId": "!!$5",
    "modifiedAt": "!!%>"
  },
  "any": {"types": ["page", "nav", "editor"], "name": "Any primitives — thoughts on UI"},
  "nav": {"pos": "PPSl", "type": 1, "parentId": ""},
  "bafyreibjoqwn23nzx63cx7n7bkunvca4jueqbuhbz27rxvjbta5fxhvyoa": {
    "3zsJKegeZJu": "PLACEHOLDER"
  },
  "author": "A9tEho5sqy7dwJXtBANYTYPEDjTJvdKjZyvP4tfb4aaV42m4",
  "createdAt": {"$date": "2026-08-27T11:48:34.000Z"},
  "spaceId": "bafyreiakg5rz2azogbfrzy3mzkku2f7sgsnprwoxlfkzmosn3lzyts5ony.3krx8ztn1ul6t",
  "modifiedAt": {"$date": "2026-08-27T11:48:39.000Z"},
  "_addSeq": 676,
  "_applySeq": 693
}`

func benchRecord(tb testing.TB) *anyenc.Value {
	tb.Helper()
	raw := strings.Replace(benchRecordJSON, "PLACEHOLDER", strings.Repeat("lorem ipsum ", 17), 1)
	doc, err := anyenc.ParseJson(raw)
	if err != nil {
		tb.Fatalf("parse bench record: %v", err)
	}
	return doc
}

// benchShaper parses a projection body the way a request would. The
// parse path writes its 400s through the context, so it gets a real
// one — a rejected body must reach the Fatalf below, not panic.
func benchShaper(tb testing.TB, body string) recordShaper {
	tb.Helper()
	if body == "" {
		return recordShaper{}
	}
	var p fastjson.Parser
	v, err := p.Parse(`{"projection":` + body + `}`)
	if err != nil {
		tb.Fatalf("parse projection: %v", err)
	}
	c, _ := newEchoCtx()
	proj, errResp, done := parseProjection(c, v, false)
	if done {
		tb.Fatalf("projection rejected: %v", errResp)
	}
	return recordShaper{proj: proj}
}

// BenchmarkShapeRecord measures the per-record cost of the
// serialisation boundary, which is where the time goes on a large
// window: finding 5 000 rows costs ~5 ms, returning them ~60 ms.
//
// "none" is the historical path — the whole record converted to
// fastjson and marshalled. The rest are the projections a client would
// actually send.
func BenchmarkShapeRecord(b *testing.B) {
	doc := benchRecord(b)
	cases := []struct {
		name string
		body string
	}{
		{"none", ""},
		{"nav-tree", `{"any":1,"nav":1}`},
		{"nav-tree-no-ver", `{"any":1,"nav":1,"_ver":-1}`},
		{"exclude-ver", `{"_ver":-1}`},
		{"exclude-ver-spaceid", `{"_ver":-1,"spaceId":-1}`},
		{"nested", `{"any.name":1,"nav.pos":1}`},
		{"nested-carve", `{"nav":1,"nav.pos":-1}`},
	}
	for _, tc := range cases {
		shaper := benchShaper(b, tc.body)
		b.Run(tc.name, func(b *testing.B) {
			fa := getFastjsonArena()
			defer putFastjsonArena(fa)
			var out []byte
			b.ReportAllocs()
			for b.Loop() {
				fa.Reset()
				out = shaper.record(doc, fa).MarshalTo(out[:0])
			}
			b.ReportMetric(float64(len(out)), "wire-B")
		})
	}
}
