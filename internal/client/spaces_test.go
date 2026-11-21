package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/anyproto/any/internal/api"
)

// TestChildSpaceRoutes pins the child-space calls to their endpoints.
func TestChildSpaceRoutes(t *testing.T) {
	ctx := context.Background()
	checkCalls(t, []struct {
		name string
		fn   func(cl *Client) error
		want call
	}{
		{"create", func(cl *Client) error {
			_, err := cl.SpaceCreate(ctx, api.SpaceCreateRequest{Name: "Team"})
			return err
		}, call{http.MethodPost, "/v1/spaces", `{"name":"Team"}`}},
		{"create a child", func(cl *Client) error {
			_, err := cl.SpaceCreate(ctx, api.SpaceCreateRequest{Name: "Design", ParentSpaceId: "parent"})
			return err
		}, call{http.MethodPost, "/v1/spaces", `{"name":"Design","parentSpaceId":"parent"}`}},
		{"retry the push", func(cl *Client) error {
			return cl.SpaceRetryPush(ctx, "child")
		}, call{http.MethodPost, "/v1/spaces/child/retry-push", ""}},
		{"list the children", func(cl *Client) error {
			_, err := cl.SpaceChildren(ctx, "parent")
			return err
		}, call{http.MethodGet, "/v1/spaces/parent/children", ""}},
		{"a child's members", func(cl *Client) error {
			_, err := cl.ChildMembers(ctx, "child")
			return err
		}, call{http.MethodGet, "/v1/children/child/members", ""}},
		{"remove a child's members", func(cl *Client) error {
			return cl.ChildRemoveMembers(ctx, "child", api.ACLRemoveRequest{Identities: []string{"A1", "A2"}})
		}, call{http.MethodPost, "/v1/children/child/acl/remove", `{"identities":["A1","A2"]}`}},
		{"change a child's owner", func(cl *Client) error {
			return cl.ChildChangeOwner(ctx, "child", api.ACLOwnershipRequest{NewOwner: "A1", OldOwnerPerm: "writer"})
		}, call{http.MethodPost, "/v1/children/child/acl/ownership", `{"newOwner":"A1","oldOwnerPerm":"writer"}`}},
		{"delete a child", func(cl *Client) error {
			return cl.ChildDelete(ctx, "child")
		}, call{http.MethodDelete, "/v1/children/child", ""}},
	})
}
