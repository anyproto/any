package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/client"
)

// decodeRecords pulls the record maps out of a /query reply.
func decodeRecords(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var resp api.QueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode query response: %v (%s)", err, body)
	}
	out := make([]map[string]any, 0, len(resp.Records))
	for _, raw := range resp.Records {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		out = append(out, m)
	}
	return out
}

// TestServer_ObjectsQueryProjection walks the projection through the
// real cross-object endpoint.
func TestServer_ObjectsQueryProjection(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	spaceId, _, _ := setupSubscribeFixture(t, e)
	url := "/v1/spaces/" + spaceId + "/objects/query"

	t.Run("no projection ships the whole record", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, url, `{}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		for _, r := range decodeRecords(t, rec.Body.Bytes()) {
			for _, want := range []string{"id", "_ver", "spaceId"} {
				if _, ok := r[want]; !ok {
					t.Errorf("unprojected record missing %q: %v", want, keys(r))
				}
			}
		}
	})

	t.Run("include mode", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, url, `{"projection":{"any":1,"nav":1}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		records := decodeRecords(t, rec.Body.Bytes())
		if len(records) == 0 {
			t.Fatal("expected at least one object")
		}
		for _, r := range records {
			if _, ok := r["id"]; !ok {
				t.Errorf("id must ride along unlisted: %v", keys(r))
			}
			for _, gone := range []string{"spaceId", "author", "createdAt", "_addSeq", "_applySeq"} {
				if _, ok := r[gone]; ok {
					t.Errorf("%q should not be projected: %v", gone, keys(r))
				}
			}
			if ver, ok := r["_ver"].(map[string]any); ok {
				if _, leaked := ver["spaceId"]; leaked {
					t.Errorf("_ver should have been narrowed with the projection: %v", keys(ver))
				}
			}
		}
	})

	t.Run("exclude _ver", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, url, `{"projection":{"_ver":-1}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		for _, r := range decodeRecords(t, rec.Body.Bytes()) {
			if _, ok := r["_ver"]; ok {
				t.Errorf("_ver should have been dropped: %v", keys(r))
			}
			// Excluding a protocol field is not a user-field inclusion,
			// so every user field still ships.
			if _, ok := r["spaceId"]; !ok {
				t.Errorf("exclude-only projection should keep user fields: %v", keys(r))
			}
		}
	})

	t.Run("projection does not change matching or order", func(t *testing.T) {
		full := decodeRecords(t, doJSON(t, e, http.MethodPost, url, `{"sort":["id"]}`).Body.Bytes())
		proj := decodeRecords(t, doJSON(t, e, http.MethodPost, url,
			`{"sort":["id"],"projection":{"nav":1}}`).Body.Bytes())
		if len(full) != len(proj) {
			t.Fatalf("projection changed the result set: %d vs %d", len(full), len(proj))
		}
		for i := range full {
			if full[i]["id"] != proj[i]["id"] {
				t.Errorf("record %d: id %v vs %v — projection must not reorder",
					i, full[i]["id"], proj[i]["id"])
			}
		}
	})

	t.Run("bad projection is a 400", func(t *testing.T) {
		rec := doJSON(t, e, http.MethodPost, url, `{"projection":{"id":-1}}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "request.invalid_field") {
			t.Errorf("unexpected error body: %s", rec.Body.String())
		}
	})
}

// TestServer_QuerySubscribeProjection: a projected subscription must
// not silently widen after the first update — the `changes` frames are
// shaped too, docs and per-field ops alike.
func TestServer_QuerySubscribeProjection(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)

	srv := httptest.NewServer(e)
	defer srv.Close()
	cl := client.New(strings.TrimPrefix(srv.URL, "http://"), 0)

	spaceId, typeId, _ := setupSubscribeFixture(t, e)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	frames := make(chan client.SSEFrame, 64)
	streamErr := make(chan error, 1)
	body, _ := json.Marshal(map[string]any{
		"projection": map[string]any{"any": 1, "nav": 1},
	})
	go func() {
		streamErr <- cl.StreamObjectsQuerySubscribe(streamCtx, spaceId, body, func(f client.SSEFrame) error {
			select {
			case frames <- f:
			case <-streamCtx.Done():
			}
			return nil
		})
	}()

	if got := waitFrame(t, frames, 5*time.Second); got.Event != "ready" {
		t.Fatalf("first frame = %q, want ready", got.Event)
	}
	snap := waitFrame(t, frames, 5*time.Second)
	if snap.Event != "snapshot" {
		t.Fatalf("second frame = %q, want snapshot", snap.Event)
	}
	var snapData api.QuerySubscribeSnapshot
	if err := json.Unmarshal(snap.Data, &snapData); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	for _, raw := range snapData.Records {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("decode snapshot record: %v", err)
		}
		if _, ok := m["spaceId"]; ok {
			t.Errorf("snapshot frame ignored the projection: %v", keys(m))
		}
	}

	// A create lands as an Added record on the same stream.
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/"+spaceId+"/objects",
		`{"type":"`+typeId+`","initialProperties":{"any":{"name":"Projected"}}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create object: %d %s", rec.Code, rec.Body.String())
	}
	var created api.ObjectsCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	ev := awaitWindowedEvent(t, frames, created.ObjectId, windowedAdded)
	for _, r := range ev.Added {
		if r.Id != created.ObjectId {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(r.Doc, &doc); err != nil {
			t.Fatalf("decode changes doc: %v", err)
		}
		if _, ok := doc["spaceId"]; ok {
			t.Errorf("changes frame widened past the projection: %v", keys(doc))
		}
		if _, ok := doc["id"]; !ok {
			t.Errorf("changes frame dropped id: %v", keys(doc))
		}
		for _, op := range r.Ops {
			if len(op.Path) > 0 && op.Path[0] != "any" && op.Path[0] != "nav" {
				t.Errorf("op outside the projection reached the wire: %v", op.Path)
			}
			if len(op.Path) == 0 && strings.Contains(string(op.Payload), "spaceId") {
				t.Errorf("multi-field op leaked an unprojected path: %s", op.Payload)
			}
		}
	}

	streamCancel()
	select {
	case <-streamErr:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not exit after cancel")
	}
}
