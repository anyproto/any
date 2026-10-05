package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// TestAddressQueryBody pins the addressing fields each scope writes
// into a query or aggregate body, and the refused combinations.
func TestAddressQueryBody(t *testing.T) {
	cases := []struct {
		name                   string
		properties, allObjects bool
		args                   []string
		dataset                string
		want                   map[string]any
		err                    string
	}{
		{"per object", false, false, []string{"sp", "obj"}, "ds", map[string]any{"objectId": "obj", "dataset": "ds"}, ""},
		{"properties", true, false, []string{"sp"}, "", map[string]any{}, ""},
		{"all objects", false, true, []string{"sp"}, "ds", map[string]any{"dataset": "ds"}, ""},
		{"all objects with an objectId", false, true, []string{"sp", "obj"}, "ds", nil, "--all-objects takes no objectId"},
		{"all objects without a dataset", false, true, []string{"sp"}, "", nil, "--dataset is required"},
		{"both scopes", true, true, []string{"sp"}, "ds", nil, "different scopes"},
		{"per object without an objectId", false, false, []string{"sp"}, "ds", nil, "objectId is required"},
		{"per object without a dataset", false, false, []string{"sp", "obj"}, "", nil, "--dataset is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := map[string]any{}
			err := addressQueryBody(body, c.properties, c.allObjects, c.args, c.dataset)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body, c.want) {
				t.Errorf("body = %v, want %v", body, c.want)
			}
		})
	}
}

// TestBuildQueryBodyAllObjects: --all-objects builds the dataset-scope
// body — the dataset and the window flags, no objectId.
func TestBuildQueryBodyAllObjects(t *testing.T) {
	raw, err := buildQueryBody(false, true, []string{"sp"}, "T_samples", `{"kind":"x"}`, "-score,id", "label,_objectId", 10, 5, true)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"dataset":      "T_samples",
		"filter":       map[string]any{"kind": "x"},
		"sort":         []any{"-score", "id"},
		"projection":   map[string]any{"label": float64(1), "_objectId": float64(1)},
		"limit":        float64(10),
		"offset":       float64(5),
		"includeTotal": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v, want %v", got, want)
	}
	if _, err := buildQueryBody(false, true, []string{"sp", "obj"}, "T_samples", "", "", "", 0, 0, false); err == nil {
		t.Error("an objectId with --all-objects must be refused")
	}
}

// cliCall is one request a command sent.
type cliCall struct {
	method, path string
	body         map[string]any
}

// cliServer records every request and answers it with reply.
func cliServer(t *testing.T, reply func(w http.ResponseWriter, r *http.Request)) (string, func() []cliCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []cliCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := cliCall{method: r.Method, path: r.URL.Path}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &c.body); err != nil {
				t.Errorf("%s %s: body %q: %v", r.Method, r.URL.Path, raw, err)
			}
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		reply(w, r)
	}))
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), func() []cliCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]cliCall(nil), calls...)
	}
}

// runCLI runs the root command with args against addr and returns its
// stdout.
func runCLI(t *testing.T, addr string, args ...string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	stdout := os.Stdout
	os.Stdout = w
	root := newRootCmd()
	root.SetArgs(append(args, "--addr", addr))
	runErr := root.ExecuteContext(context.Background())
	os.Stdout = stdout
	_ = w.Close()
	return <-out, runErr
}

// TestAllObjectsCommands: `query-subscribe --all-objects` and
// `aggregate --all-objects` POST the dataset-scope routes with the
// dataset and no objectId, and print the server's reply.
func TestAllObjectsCommands(t *testing.T) {
	addr, calls := cliServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/spaces/sp1/datasets/query/subscribe":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: ready\ndata: {}\n\n"+
				`event: snapshot`+"\n"+`data: {"records":[{"id":"o1/r1","_objectId":"o1"}]}`+"\n\n")
		case "/v1/spaces/sp1/datasets/aggregate":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"records":[{"id":"o1","n":2}]}`)
		default:
			http.NotFound(w, r)
		}
	})

	out, err := runCLI(t, addr, "query-subscribe", "sp1", "--all-objects", "--dataset", "T_samples",
		"--filter", `{"kind":"x"}`, "--sort", "-score", "--limit", "5", "--total")
	if err != nil {
		t.Fatalf("query-subscribe: %v", err)
	}
	if !strings.Contains(out, `{"event":"ready"`) || !strings.Contains(out, `"o1/r1"`) {
		t.Errorf("query-subscribe output = %q", out)
	}
	out, err = runCLI(t, addr, "aggregate", "sp1", "--all-objects", "--dataset", "T_samples", "--explain",
		"--pipeline", `[{"$group":{"_id":"$_objectId","n":{"$count":{}}}}]`)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if !strings.Contains(out, `"n": 2`) {
		t.Errorf("aggregate output = %q", out)
	}

	got := calls()
	want := []cliCall{
		{http.MethodPost, "/v1/spaces/sp1/datasets/query/subscribe", map[string]any{
			"dataset": "T_samples", "filter": map[string]any{"kind": "x"},
			"sort": []any{"-score"}, "limit": float64(5), "includeTotal": true,
		}},
		{http.MethodPost, "/v1/spaces/sp1/datasets/aggregate", map[string]any{
			"dataset": "T_samples", "explain": true,
			"pipeline": []any{map[string]any{"$group": map[string]any{"_id": "$_objectId", "n": map[string]any{"$count": map[string]any{}}}}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %+v\nwant %+v", got, want)
	}

	// Refused before any request.
	for _, args := range [][]string{
		{"query-subscribe", "sp1", "obj", "--all-objects", "--dataset", "T_samples"},
		{"query-subscribe", "sp1", "--all-objects"},
		{"aggregate", "sp1", "--all-objects", "--properties", "--dataset", "T_samples", "--pipeline", `[]`},
		{"aggregate", "sp1", "obj", "--all-objects", "--dataset", "T_samples", "--pipeline", `[]`},
	} {
		if _, err := runCLI(t, addr, args...); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
	if n := len(calls()); n != 2 {
		t.Errorf("refused commands sent requests: %+v", calls()[2:])
	}
}

// TestTypeDatasetIndexCommands: `type part dataset index add|remove`
// call the declared-index routes with the draft as the body.
func TestTypeDatasetIndexCommands(t *testing.T) {
	addr, calls := cliServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"indexDefId":"ix1"}`)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	out, err := runCLI(t, addr, "type", "part", "dataset", "index", "add", "sp1", "ty1", "def1",
		"--index", `{"key":"by_start","fields":["start","_objectId"]}`)
	if err != nil {
		t.Fatalf("index add: %v", err)
	}
	if !strings.Contains(out, `"indexDefId": "ix1"`) {
		t.Errorf("index add output = %q", out)
	}
	if _, err := runCLI(t, addr, "type", "part", "dataset", "index", "remove", "sp1", "ty1", "def1", "ix1"); err != nil {
		t.Fatalf("index remove: %v", err)
	}
	want := []cliCall{
		{http.MethodPost, "/v1/spaces/sp1/types/ty1/datasets/def1/indexes", map[string]any{
			"key": "by_start", "fields": []any{"start", "_objectId"},
		}},
		{http.MethodDelete, "/v1/spaces/sp1/types/ty1/datasets/def1/indexes/ix1", nil},
	}
	if got := calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %+v\nwant %+v", got, want)
	}
}
