package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bao"
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

// claimRoot writes a competing root into the bundle's add-only roots
// set, the shape a concurrent install on another device produces once
// its change arrives. Written through the generic dataset surface
// because the typed API deliberately has no "register someone else's
// root" call.
func claimRoot(t *testing.T, sp space.Space, bundleId, rootId string) {
	t.Helper()
	res, err := sp.Modify(context.Background(), space.ModifyBatch{
		ObjectId: sp.SpaceIndexObjectId(),
		Dataset:  "bundles",
		Records: []space.RecordModify{{
			Id:  bundleId,
			Ops: []space.Op{{Type: space.OpAddToSet, Path: "roots", Value: rootId}},
		}},
	})
	if err != nil {
		t.Fatalf("claim root: %v", err)
	}
	if len(res.Rejections) > 0 {
		t.Fatalf("claim root rejected: %s", res.Rejections[0].Reason)
	}
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

// TestServer_GeneralChat_LoserResolved covers the conflict path: a
// competing root claimed by "another device" surfaces as a loser and
// is resolved (cascade-deleted) on the next space read, leaving the
// winner untouched.
func TestServer_GeneralChat_LoserResolved(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	created := createSpaceInfo(t, e, "GeneralChatLoser")
	sp, err := d.sdk.Spaces().Get(ctx, created.Id)
	if err != nil {
		t.Fatalf("get space: %v", err)
	}

	loser, err := sp.Objects().Create(ctx, space.CreateObjectOpts{Types: []string{chat.TypeId}})
	if err != nil {
		t.Fatalf("create competing root: %v", err)
	}
	claimRoot(t, sp, chat.GeneralChatBundleId, loser)

	b, err := sp.Bundles().Get(ctx, chat.GeneralChatBundleId)
	if err != nil {
		t.Fatalf("bundle get: %v", err)
	}
	if !slices.Contains(b.Losers, loser) {
		t.Fatalf("competing root not reported as loser: %+v", b)
	}

	// A space read resolves it — the loser carries no messages, so
	// nothing is worth keeping.
	var got api.SpaceInfo
	decodeGet(t, e, "/v1/spaces/"+created.Id, &got)
	if got.GeneralChatObjectId != created.GeneralChatObjectId {
		t.Fatalf("winner changed: %q vs %q", got.GeneralChatObjectId, created.GeneralChatObjectId)
	}

	after, err := sp.Bundles().Get(ctx, chat.GeneralChatBundleId)
	if err != nil {
		t.Fatalf("bundle get after: %v", err)
	}
	if len(after.Losers) != 0 {
		t.Fatalf("loser not resolved: %+v", after.Losers)
	}
	// The claim itself is never retracted — the roots set is the
	// add-only audit trail.
	if !slices.Contains(after.Roots, loser) {
		t.Fatalf("resolved loser dropped from roots: %+v", after.Roots)
	}
}

// TestServer_GeneralChat_LoserWithMessagesKept pins the Keep guard:
// chat messages cannot be merged (a copy re-attributes them), so a
// loser that was written to is left alone instead of deleted.
func TestServer_GeneralChat_LoserWithMessagesKept(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	ctx := context.Background()

	created := createSpaceInfo(t, e, "GeneralChatKeep")
	sp, err := d.sdk.Spaces().Get(ctx, created.Id)
	if err != nil {
		t.Fatalf("get space: %v", err)
	}

	loser, err := sp.Objects().Create(ctx, space.CreateObjectOpts{Types: []string{chat.TypeId}})
	if err != nil {
		t.Fatalf("create competing root: %v", err)
	}
	if msg := chatSend(t, e, "/v1/spaces/"+created.Id+"/objects/"+loser, "written before the merge", ""); msg.Id == "" {
		t.Fatalf("send to competing root: %+v", msg)
	}
	claimRoot(t, sp, chat.GeneralChatBundleId, loser)

	var got api.SpaceInfo
	decodeGet(t, e, "/v1/spaces/"+created.Id, &got)

	after, err := sp.Bundles().Get(ctx, chat.GeneralChatBundleId)
	if err != nil {
		t.Fatalf("bundle get after: %v", err)
	}
	if !slices.Contains(after.Losers, loser) {
		t.Fatalf("written-to loser was resolved away: %+v", after)
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
