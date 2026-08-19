package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bao"
	"github.com/anyproto/any/internal/bundles"
	"github.com/anyproto/any/internal/chat"
)

// createSpaceInfo creates a space over HTTP and returns its SpaceInfo.
func createSpaceInfo(t *testing.T, e http.Handler, name string) api.SpaceInfo {
	t.Helper()
	rec := doJSON(t, e, http.MethodPost, "/v1/spaces", `{"name":"`+name+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create space: %d %s", rec.Code, rec.Body.String())
	}
	var info api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	return info
}

// TestServer_GeneralChat_Bundle pins the general chat onto the bundles
// registry: the id reported on SpaceInfo is the bundle's winning root,
// it is stable across reads, and the registry claims it.
func TestServer_GeneralChat_Bundle(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	created := createSpaceInfo(t, e, "GeneralChatBundle")
	if created.GeneralChatObjectId == "" {
		t.Fatalf("create response missing generalChatObjectId: %+v", created)
	}

	sp, err := d.sdk.Spaces().Get(ctx, created.Id)
	if err != nil {
		t.Fatalf("get space: %v", err)
	}
	b, err := sp.Bundles().Get(ctx, chat.GeneralChatBundleId)
	if err != nil {
		t.Fatalf("bundle get: %v", err)
	}
	if b.RootId != created.GeneralChatObjectId {
		t.Fatalf("bundle root %q != generalChatObjectId %q", b.RootId, created.GeneralChatObjectId)
	}
	if !slices.Contains(b.Roots, b.RootId) {
		t.Fatalf("winner not claimed in roots: %+v", b)
	}
	if len(b.Losers) != 0 {
		t.Fatalf("unexpected losers on a single-device install: %+v", b.Losers)
	}

	// Adoption, not reinstall: further reads return the same winner.
	var got api.SpaceInfo
	decodeGet(t, e, "/v1/spaces/"+created.Id, &got)
	if got.GeneralChatObjectId != created.GeneralChatObjectId {
		t.Fatalf("chat id not stable: %q vs %q", got.GeneralChatObjectId, created.GeneralChatObjectId)
	}
}

// TestServer_DerivedSpace_BaoBundle covers the creation side of the
// setup split: materializing the bao space installs its bundle, and
// repeating the call adopts the same root instead of installing again.
func TestServer_DerivedSpace_BaoBundle(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	rec := doJSON(t, e, http.MethodPost, "/v1/spaces/derived/bao", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("materialize bao: %d %s", rec.Code, rec.Body.String())
	}
	var info api.SpaceInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	sp, err := d.sdk.Spaces().Get(ctx, info.Id)
	if err != nil {
		t.Fatalf("get space: %v", err)
	}
	b, err := sp.Bundles().Get(ctx, bao.BundleId)
	if err != nil {
		t.Fatalf("bao bundle get: %v", err)
	}
	if b.RootId == "" {
		t.Fatalf("bao bundle has no root: %+v", b)
	}
	if b.Name != bao.BundleName {
		t.Fatalf("bao bundle name %q", b.Name)
	}

	rec = doJSON(t, e, http.MethodPost, "/v1/spaces/derived/bao", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("re-materialize bao: %d %s", rec.Code, rec.Body.String())
	}
	again, err := sp.Bundles().Get(ctx, bao.BundleId)
	if err != nil {
		t.Fatalf("bao bundle get again: %v", err)
	}
	if again.RootId != b.RootId {
		t.Fatalf("bao root not adopted: %q vs %q", again.RootId, b.RootId)
	}
	if len(again.Roots) != 1 {
		t.Fatalf("bao install repeated: %+v", again.Roots)
	}
}

// TestServer_BundleChild covers the setup-child mechanism: a child
// derived under a bundle root is deterministic per (root, seed), and
// deriving it again returns the same object rather than a second one.
func TestServer_BundleChild(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	created := createSpaceInfo(t, e, "BundleChild")
	sp, err := d.sdk.Spaces().Get(ctx, created.Id)
	if err != nil {
		t.Fatalf("get space: %v", err)
	}
	b, err := sp.Bundles().Get(ctx, chat.GeneralChatBundleId)
	if err != nil {
		t.Fatalf("bundle get: %v", err)
	}

	first, err := bundles.Child(ctx, sp, b.RootId, "test/child/v1", chat.TypeId)
	if err != nil {
		t.Fatalf("child: %v", err)
	}
	again, err := bundles.Child(ctx, sp, b.RootId, "test/child/v1", chat.TypeId)
	if err != nil {
		t.Fatalf("child again: %v", err)
	}
	if first != again {
		t.Fatalf("child id not deterministic: %q vs %q", first, again)
	}
	if first == b.RootId {
		t.Fatal("child collided with its root")
	}

	// A different seed under the same root is a different object.
	other, err := bundles.Child(ctx, sp, b.RootId, "test/other/v1")
	if err != nil {
		t.Fatalf("second child: %v", err)
	}
	if other == first {
		t.Fatalf("distinct seeds derived the same child: %q", other)
	}

	// An install with no winner has no root to hang children off.
	if _, err := bundles.Child(ctx, sp, "", "test/child/v1"); err == nil {
		t.Fatal("child accepted an empty root id")
	}
}
