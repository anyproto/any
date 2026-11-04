package server

import (
	"context"
	"errors"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/bundles"
)

// handleSpace is the slice of a space the handle check reads: the
// account's role, the handles in the space, the bundle's registry row
// (absent here) and the id its derived root would have.
type handleSpace struct {
	space.Space
	types   []space.TypeInfo
	derived string
}

func (s *handleSpace) Info() space.SpaceInfo {
	return space.SpaceInfo{Id: "sp", OwnRole: space.PermissionWriter}
}
func (s *handleSpace) SpaceIndexObjectId() string        { return "index" }
func (s *handleSpace) Types() space.TypesAPI             { return &handleTypes{infos: s.types} }
func (s *handleSpace) Collections() space.CollectionsAPI { return &handleCollections{} }
func (s *handleSpace) Bundles() space.BundlesAPI         { return &handleBundles{derived: s.derived} }
func (s *handleSpace) Query(string, string) space.Query  { return &handleQuery{} }

type handleTypes struct {
	space.TypesAPI
	infos []space.TypeInfo
}

func (t *handleTypes) List(context.Context) ([]space.TypeInfo, error) { return t.infos, nil }

type handleCollections struct{ space.CollectionsAPI }

func (*handleCollections) List(context.Context) ([]space.CollectionInfo, error) { return nil, nil }

type handleBundles struct {
	space.BundlesAPI
	derived string
}

func (b *handleBundles) DerivedRootId(context.Context, string) (string, error) { return b.derived, nil }

type handleQuery struct{ space.Query }

func (q *handleQuery) Filter(any) space.Query                   { return q }
func (*handleQuery) One(context.Context) (*anyenc.Value, error) { return nil, space.ErrNotFound }

// A derived bundle's own root can hold the handle before the registry
// row that names it has synced; it is never a conflict. Another type
// holding the handle still is.
func TestCatalogBeforeInstall_DerivedRootHoldsItsHandle(t *testing.T) {
	inst := bundles.Install{Id: "system:general-chat/v1", XKey: "general_chat", Derived: true}
	sp := &handleSpace{derived: "derived-root", types: []space.TypeInfo{{Id: "derived-root", XKey: "general_chat"}}}
	if err := catalogBeforeInstall(context.Background(), sp, inst); err != nil {
		t.Fatalf("own derived root reported as a conflict: %v", err)
	}
	sp.types = []space.TypeInfo{{Id: "user-type", XKey: "general_chat"}}
	var conflict *xKeyConflictError
	if err := catalogBeforeInstall(context.Background(), sp, inst); !errors.As(err, &conflict) || conflict.ExistingTypeId != "user-type" {
		t.Fatalf("a user type holding the handle: %v", err)
	}
	// A created bundle gets no derived exemption.
	inst.Derived = false
	sp.types = []space.TypeInfo{{Id: "derived-root", XKey: "general_chat"}}
	if err := catalogBeforeInstall(context.Background(), sp, inst); !errors.As(err, &conflict) {
		t.Fatalf("created install past a foreign holder: %v", err)
	}
}
