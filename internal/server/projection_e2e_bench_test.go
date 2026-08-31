package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// benchSeedObjects is how many objects the end-to-end projection
// benchmark seeds. anyproto/any#203 measured 5 000; seeding that many
// through the real CRDT write path would dominate the run, and the
// shaping cost is linear in the record count, so a smaller space
// measures the same ratio.
const benchSeedObjects = 400

// BenchmarkObjectsQueryProjection is the end-to-end half of the
// before/after: the whole POST /objects/query handler, store read
// included, so the per-record shaping win from BenchmarkShapeRecord can
// be read as a fraction of a real request rather than in isolation.
func BenchmarkObjectsQueryProjection(b *testing.B) {
	d, teardown := newTestDeps(b)
	defer teardown()
	e := buildEcho(d)

	spaceId := benchSeedSpace(b, e)

	cases := []struct {
		name string
		body string
	}{
		{"none", `{}`},
		{"nav-tree", `{"projection":{"any":1,"nav":1}}`},
		{"nav-tree-no-ver", `{"projection":{"any":1,"nav":1,"_ver":-1}}`},
		{"exclude-ver", `{"projection":{"_ver":-1}}`},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			var size int
			b.ReportAllocs()
			for b.Loop() {
				r := httptest.NewRequest(http.MethodPost, "/v1/spaces/"+spaceId+"/objects/query",
					strings.NewReader(tc.body))
				r.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, r)
				if rec.Code != http.StatusOK {
					b.Fatalf("status %d: %s", rec.Code, rec.Body.String())
				}
				size = rec.Body.Len()
			}
			b.ReportMetric(float64(size)/1024, "wire-KiB")
		})
	}
}

// benchSeedSpace creates a space with benchSeedObjects objects, each
// carrying a name and one ~200-char string property — the record shape
// the issue measured.
func benchSeedSpace(b *testing.B, e http.Handler) string {
	b.Helper()

	rec := postJSON(b, e, "/v1/spaces", `{"name":"ProjBench"}`, http.StatusCreated)
	var sp api.SpaceInfo
	mustDecode(b, rec.Body.Bytes(), &sp)

	rec = postJSON(b, e, "/v1/spaces/"+sp.Id+"/types",
		`{"name":"Note","xKey":"note"}`, http.StatusCreated)
	var tr api.TypesCreateResponse
	mustDecode(b, rec.Body.Bytes(), &tr)

	rec = postJSON(b, e, "/v1/spaces/"+sp.Id+"/types/"+tr.TypeId+"/properties",
		`{"name":"Body","kind":"string","xKey":"body"}`, http.StatusCreated)
	var pr api.AddPropertyResponse
	mustDecode(b, rec.Body.Bytes(), &pr)

	body := strings.Repeat("lorem ipsum ", 17)
	for i := range benchSeedObjects {
		payload := fmt.Sprintf(
			`{"types":[%q],"initialProperties":{"any":{"name":"Note %d"},%q:{%q:%q}}}`,
			tr.TypeId, i, tr.TypeId, pr.PropId, body)
		postJSON(b, e, "/v1/spaces/"+sp.Id+"/objects", payload, http.StatusCreated)
	}
	return sp.Id
}

func postJSON(b *testing.B, e http.Handler, path, body string, want int) *httptest.ResponseRecorder {
	b.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	if rec.Code != want {
		b.Fatalf("POST %s → %d (want %d): %s", path, rec.Code, want, rec.Body.String())
	}
	return rec
}

func mustDecode(b *testing.B, data []byte, v any) {
	b.Helper()
	if err := json.Unmarshal(data, v); err != nil {
		b.Fatalf("decode: %v (%s)", err, data)
	}
}
