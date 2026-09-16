package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestObjectRoutes pins the create body — one type, many collections —
// and the three binding calls behind it. There is no unset: every
// object has exactly one type.
func TestObjectRoutes(t *testing.T) {
	ctx := context.Background()
	checkCalls(t, []struct {
		name string
		fn   func(cl *Client) error
		want call
	}{
		{"create", func(cl *Client) error {
			_, err := cl.ObjectsCreate(ctx, "sp", api.ObjectCreateRequest{
				Type:        "person",
				Collections: []string{"contact", "investor"},
			})
			return err
		}, call{http.MethodPost, "/v1/spaces/sp/objects", `{"type":"person","collections":["contact","investor"]}`}},
		{"set type", func(cl *Client) error {
			_, err := cl.ObjectSetType(ctx, "sp", "o1", "person")
			return err
		}, call{http.MethodPost, "/v1/spaces/sp/properties/o1/type/person", ""}},
		{"attach collection", func(cl *Client) error {
			_, err := cl.ObjectAttachCollection(ctx, "sp", "o1", "bin")
			return err
		}, call{http.MethodPost, "/v1/spaces/sp/properties/o1/collections/bin", ""}},
		{"detach collection", func(cl *Client) error {
			_, err := cl.ObjectDetachCollection(ctx, "sp", "o1", "bin")
			return err
		}, call{http.MethodDelete, "/v1/spaces/sp/properties/o1/collections/bin", ""}},
	})
}
