package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anyproto/any/internal/api"
)

// call is the whole contract of a client method: the request it makes.
type call struct {
	method string
	path   string
	body   string
}

// record runs fn against a stub server that answers 204 to everything
// and returns the request fn produced.
func record(t *testing.T, fn func(cl *Client) error) call {
	t.Helper()
	var got call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		got = call{r.Method, r.URL.RequestURI(), string(b)}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cl := New(strings.TrimPrefix(srv.URL, "http://"), 5*time.Second)
	if err := fn(cl); err != nil {
		t.Fatalf("call: %v", err)
	}
	return got
}

func checkCalls(t *testing.T, cases []struct {
	name string
	fn   func(cl *Client) error
	want call
},
) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := record(t, tc.fn)
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestCollectionRoutes pins every collection call to its endpoint: the
// type routes with a collections segment.
func TestCollectionRoutes(t *testing.T) {
	ctx := context.Background()
	name := "Wiki"
	checkCalls(t, []struct {
		name string
		fn   func(cl *Client) error
		want call
	}{
		{"create", func(cl *Client) error {
			_, err := cl.CollectionsCreate(ctx, "sp", api.CollectionsCreateRequest{Name: name, XKey: "wiki"})
			return err
		}, call{http.MethodPost, "/v1/spaces/sp/collections", `{"name":"Wiki","xKey":"wiki"}`}},
		{"list", func(cl *Client) error {
			_, err := cl.CollectionsList(ctx, "sp", false)
			return err
		}, call{http.MethodGet, "/v1/spaces/sp/collections", ""}},
		{"list include hidden", func(cl *Client) error {
			_, err := cl.CollectionsList(ctx, "sp", true)
			return err
		}, call{http.MethodGet, "/v1/spaces/sp/collections?includeHidden=true", ""}},
		{"get", func(cl *Client) error {
			_, err := cl.CollectionGet(ctx, "sp", "wiki")
			return err
		}, call{http.MethodGet, "/v1/spaces/sp/collections/wiki", ""}},
		{"patch", func(cl *Client) error {
			return cl.CollectionPatch(ctx, "sp", "wiki", api.CollectionPatchRequest{Name: &name})
		}, call{http.MethodPatch, "/v1/spaces/sp/collections/wiki", `{"name":"Wiki"}`}},
		{"list properties", func(cl *Client) error {
			_, err := cl.CollectionListProperties(ctx, "sp", "wiki")
			return err
		}, call{http.MethodGet, "/v1/spaces/sp/collections/wiki/properties", ""}},
		{"add property", func(cl *Client) error {
			_, err := cl.CollectionAddProperty(ctx, "sp", "wiki", api.AddPropertyRequest{XKey: "pos", Kind: api.PropertyKindString})
			return err
		}, call{http.MethodPost, "/v1/spaces/sp/collections/wiki/properties", `{"xKey":"pos","kind":"string"}`}},
		{"patch property", func(cl *Client) error {
			return cl.CollectionPatchProperty(ctx, "sp", "wiki", "p1", api.PropertyPatchRequest{Unset: []string{"xFormat"}})
		}, call{http.MethodPatch, "/v1/spaces/sp/collections/wiki/properties/p1", `{"unset":["xFormat"]}`}},
		{"remove property", func(cl *Client) error {
			return cl.CollectionRemoveProperty(ctx, "sp", "wiki", "p1")
		}, call{http.MethodDelete, "/v1/spaces/sp/collections/wiki/properties/p1", ""}},
	})
}
