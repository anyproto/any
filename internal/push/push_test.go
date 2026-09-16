package push

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"
)

// fakePushAPI is a func-field stub for the SDK's space.PushAPI —
// injected via the Service.pushAPI test seam. Unset methods no-op.
type fakePushAPI struct {
	setToken      func(ctx context.Context, platform space.PushPlatform, token string) error
	registerSpace func(ctx context.Context, spaceId string) error
	subscribeAll  func(ctx context.Context, subs []space.PushSpaceTopics) error
}

func (f *fakePushAPI) SetToken(ctx context.Context, platform space.PushPlatform, token string) error {
	if f.setToken != nil {
		return f.setToken(ctx, platform, token)
	}
	return nil
}

func (f *fakePushAPI) RevokeToken(context.Context) error { return nil }

func (f *fakePushAPI) RegisterSpace(ctx context.Context, spaceId string) error {
	if f.registerSpace != nil {
		return f.registerSpace(ctx, spaceId)
	}
	return nil
}

func (f *fakePushAPI) RemoveSpace(context.Context, string) error { return nil }

func (f *fakePushAPI) SubscribeAll(ctx context.Context, subs []space.PushSpaceTopics) error {
	if f.subscribeAll != nil {
		return f.subscribeAll(ctx, subs)
	}
	return nil
}

func (f *fakePushAPI) Subscriptions(context.Context) ([]space.PushSubscription, error) {
	return nil, nil
}

func (f *fakePushAPI) Notify(context.Context, string, []string, []byte, string) error {
	return nil
}

func (f *fakePushAPI) NotifySilent(context.Context, string, string) error { return nil }

func (s *Service) forwarded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenForwarded
}

// --- F3: token-forward vs concurrent SetToken race --------------------

// TestEnsureToken_ConcurrentSetTokenNotMasked: a SetToken that lands
// while ensureToken's forward of the OLD token is in flight (and whose
// own synchronous forward failed transiently) must not be masked by
// the stale forward succeeding — tokenForwarded stays false so the
// next round re-forwards the new token.
func TestEnsureToken_ConcurrentSetTokenNotMasked(t *testing.T) {
	s := New(nil, t.TempDir())
	fake := &fakePushAPI{}
	s.pushAPI = fake

	s.mu.Lock()
	s.token = &deviceToken{Platform: "ios", Token: "A"}
	s.tokenForwarded = false
	s.mu.Unlock()

	fake.setToken = func(_ context.Context, _ space.PushPlatform, token string) error {
		if token != "A" {
			t.Fatalf("first round must forward the snapshot A, got %q", token)
		}
		// Concurrent SetToken("B") mid-flight: persists the new token,
		// but its own synchronous forward failed transiently.
		s.mu.Lock()
		s.token = &deviceToken{Platform: "ios", Token: "B"}
		s.tokenForwarded = false
		s.mu.Unlock()
		return nil // the STALE forward of "A" succeeds
	}
	s.ensureToken(context.Background())
	if s.forwarded() {
		t.Fatal("stale forward of A masked the concurrent SetToken(B) — B would never re-forward")
	}

	// The next round forwards the new token and marks it done.
	var sent []string
	fake.setToken = func(_ context.Context, _ space.PushPlatform, token string) error {
		sent = append(sent, token)
		return nil
	}
	s.ensureToken(context.Background())
	if !reflect.DeepEqual(sent, []string{"B"}) {
		t.Fatalf("second round forwarded %v, want [B]", sent)
	}
	if !s.forwarded() {
		t.Fatal("clean forward must mark the token forwarded")
	}

	// And once forwarded, further rounds are no-ops.
	sent = nil
	s.ensureToken(context.Background())
	if len(sent) != 0 {
		t.Fatalf("forwarded token must not re-forward, sent %v", sent)
	}
}

// TestSetToken_ConcurrentChangeNotMasked: the same re-check guards
// SetToken's own synchronous-forward success path.
func TestSetToken_ConcurrentChangeNotMasked(t *testing.T) {
	s := New(nil, t.TempDir())
	fake := &fakePushAPI{}
	s.pushAPI = fake

	fake.setToken = func(_ context.Context, _ space.PushPlatform, token string) error {
		if token == "A" {
			// Concurrent SetToken("B") wins the persist while A's
			// forward is in flight.
			s.mu.Lock()
			s.token = &deviceToken{Platform: "android", Token: "B"}
			s.tokenForwarded = false
			s.mu.Unlock()
		}
		return nil
	}
	if err := s.SetToken(context.Background(), space.PushPlatformAndroid, "A"); err != nil {
		t.Fatal(err)
	}
	if s.forwarded() {
		t.Fatal("A's stale forward must not mark B forwarded")
	}
}

// --- F2: RegisterSpace warn-and-continue -------------------------------

// TestReconcile_RegisterSpaceFailureContinues: one unregistrable space
// must not starve the account — the loop continues to the remaining
// registrations and the failed space KEEPS its topics in the
// SubscribeAll payload (registration is best-effort forward-compat).
func TestReconcile_RegisterSpaceFailureContinues(t *testing.T) {
	s := New(nil, t.TempDir())
	fake := &fakePushAPI{}
	s.pushAPI = fake

	var registered []string
	fake.registerSpace = func(_ context.Context, id string) error {
		registered = append(registered, id)
		if id == "bad" {
			return errors.New("push node rejected the space key")
		}
		return nil
	}
	var gotSubs []space.PushSpaceTopics
	subscribeCalls := 0
	fake.subscribeAll = func(_ context.Context, subs []space.PushSpaceTopics) error {
		subscribeCalls++
		gotSubs = subs
		return nil
	}

	subs := []space.PushSpaceTopics{
		{SpaceId: "bad", Topics: []string{TopicChats, testIdentity}},
		{SpaceId: "good", Topics: []string{TopicChats, testIdentity}},
	}
	if !s.reconcile(context.Background(), []string{"bad", "good"}, subs) {
		t.Fatal("a per-space registration failure must not fail the round")
	}
	if want := []string{"bad", "good"}; !reflect.DeepEqual(registered, want) {
		t.Errorf("registered = %v, want %v (must continue past the failure)", registered, want)
	}
	if subscribeCalls != 1 || !reflect.DeepEqual(gotSubs, subs) {
		t.Errorf("SubscribeAll = %d calls with %+v, want 1 call with the full payload %+v",
			subscribeCalls, gotSubs, subs)
	}
}

// TestReconcile_NotConfiguredAborts: ErrPushNotConfigured is terminal
// — the round aborts before SubscribeAll and the loop idles.
func TestReconcile_NotConfiguredAborts(t *testing.T) {
	s := New(nil, t.TempDir())
	fake := &fakePushAPI{}
	s.pushAPI = fake
	fake.registerSpace = func(context.Context, string) error { return space.ErrPushNotConfigured }
	subscribed := false
	fake.subscribeAll = func(context.Context, []space.PushSpaceTopics) error {
		subscribed = true
		return nil
	}
	if s.reconcile(context.Background(), []string{"sp1"}, nil) {
		t.Fatal("not-configured must fail the round")
	}
	if subscribed {
		t.Error("SubscribeAll must not run after not-configured")
	}
	s.mu.Lock()
	idle := s.notConfigured
	s.mu.Unlock()
	if !idle {
		t.Error("not-configured must idle the loop")
	}
}

// TestReconcile_SubscribeAllFailureFailsRound: a failed SubscribeAll
// still fails the round (the hash must not advance).
func TestReconcile_SubscribeAllFailureFailsRound(t *testing.T) {
	s := New(nil, t.TempDir())
	fake := &fakePushAPI{}
	s.pushAPI = fake
	fake.subscribeAll = func(context.Context, []space.PushSpaceTopics) error {
		return errors.New("transient")
	}
	if s.reconcile(context.Background(), nil, []space.PushSpaceTopics{{SpaceId: "sp1", Topics: []string{TopicChats}}}) {
		t.Fatal("a failed SubscribeAll must fail the round")
	}
}

// --- F4: collectChatModes fail-safe -------------------------------------

// TestCollectChatModes_FailSafeCache: a failing enumeration reuses the
// last-known-good entries — a muted chat stays muted, never degrading
// to bulk topics.
func TestCollectChatModes_FailSafeCache(t *testing.T) {
	s := New(nil, t.TempDir())
	infos := []space.SpaceInfo{activeSpace("sp1", ModeAll)}
	good := []chatNotify{{objectId: "c1", mode: ModeNone}}

	fail := false
	s.chatEnum = func(_ context.Context, spaceId string) ([]chatNotify, error) {
		if spaceId != "sp1" {
			t.Fatalf("unexpected space %q", spaceId)
		}
		if fail {
			return nil, errors.New("open space: storage wedged")
		}
		return good, nil
	}

	// Round 1: success primes the cache.
	out, omit := s.collectChatModes(context.Background(), infos)
	if len(omit) != 0 {
		t.Fatalf("healthy round must omit nothing: %v", omit)
	}
	if !reflect.DeepEqual(out["sp1"], good) {
		t.Fatalf("round 1 entries = %+v", out)
	}

	// Round 2: failure falls back to the cached entries.
	fail = true
	out, omit = s.collectChatModes(context.Background(), infos)
	if len(omit) != 0 {
		t.Fatalf("cached space must not be omitted: %v", omit)
	}
	if !reflect.DeepEqual(out["sp1"], good) {
		t.Fatalf("failure must reuse last-known-good entries (mute preserved), got %+v", out)
	}
	// The desired topics from the cached entries keep c1 muted.
	subs, _ := desiredSubs(infos, out, testIdentity)
	if len(subs) != 0 {
		t.Fatalf("muted chat resurfaced as %+v — mute violated", subs)
	}
}

// TestCollectChatModes_NoCacheOmits: a failing space with no cache is
// omitted from the desired set entirely (NOT bulk), and the omission
// is hash-visible so a recovered space re-subscribes.
func TestCollectChatModes_NoCacheOmits(t *testing.T) {
	s := New(nil, t.TempDir())
	s.chatEnum = func(context.Context, string) ([]chatNotify, error) {
		return nil, errors.New("boom")
	}
	infos := []space.SpaceInfo{activeSpace("sp1", ModeAll)}
	out, omit := s.collectChatModes(context.Background(), infos)
	if len(out) != 0 {
		t.Fatalf("omitted space must contribute no entries: %+v", out)
	}
	if !omit["sp1"] {
		t.Fatalf("uncached failing space must be omitted, got %v", omit)
	}

	// Omission changes the desired hash vs the healthy state — the
	// partial round never records the healthy hash, so recovery always
	// re-subscribes.
	fullSubs, fullReg := desiredSubs(infos, nil, testIdentity)
	var kept []space.SpaceInfo
	for _, info := range infos {
		if !omit[info.Id] {
			kept = append(kept, info)
		}
	}
	partSubs, partReg := desiredSubs(kept, nil, testIdentity)
	if len(partSubs) != 0 {
		t.Fatalf("omitted space leaked topics: %+v (bulk fallback would unmute muted chats)", partSubs)
	}
	if desiredHash(fullReg, fullSubs) == desiredHash(partReg, partSubs) {
		t.Fatal("omission must be visible in desiredHash")
	}
}

// TestCollectChatModes_EmptySuccessIsCached: "no chats" is a valid
// last-known-good state — after a successful empty enumeration a
// failure falls back to it (bulk topics, no omission).
func TestCollectChatModes_EmptySuccessIsCached(t *testing.T) {
	s := New(nil, t.TempDir())
	infos := []space.SpaceInfo{activeSpace("sp1", ModeAll)}

	fail := false
	s.chatEnum = func(context.Context, string) ([]chatNotify, error) {
		if fail {
			return nil, errors.New("boom")
		}
		return nil, nil // success: zero chats
	}
	if _, omit := s.collectChatModes(context.Background(), infos); len(omit) != 0 {
		t.Fatalf("empty success must not omit: %v", omit)
	}
	fail = true
	out, omit := s.collectChatModes(context.Background(), infos)
	if len(omit) != 0 {
		t.Fatalf("cached-empty space must not be omitted: %v", omit)
	}
	if len(out) != 0 {
		t.Fatalf("cached-empty space must contribute no entries: %+v", out)
	}
	// Bulk topics survive — the last-known-good state had no chats.
	subs, _ := desiredSubs(infos, out, testIdentity)
	if len(subs) != 1 || !reflect.DeepEqual(subs[0].Topics, []string{TopicChats, testIdentity}) {
		t.Fatalf("bulk topics expected from cached-empty state: %+v", subs)
	}
}

// TestCollectChatModes_PruneOnInactive: cache entries follow the
// active space set — a space that left the list is pruned, so its
// stale modes can never resurrect after it returns.
func TestCollectChatModes_PruneOnInactive(t *testing.T) {
	s := New(nil, t.TempDir())
	sp1 := activeSpace("sp1", ModeAll)

	fail := false
	s.chatEnum = func(context.Context, string) ([]chatNotify, error) {
		if fail {
			return nil, errors.New("boom")
		}
		return []chatNotify{{objectId: "c1", mode: ModeNone}}, nil
	}
	// Prime the cache, then run a round without sp1 — pruned.
	s.collectChatModes(context.Background(), []space.SpaceInfo{sp1})
	s.collectChatModes(context.Background(), nil)
	if _, ok := s.cachedChatModes("sp1"); ok {
		t.Fatal("cache must be pruned when the space leaves the active set")
	}
	// sp1 comes back but fails: no stale cache → omitted.
	fail = true
	_, omit := s.collectChatModes(context.Background(), []space.SpaceInfo{sp1})
	if !omit["sp1"] {
		t.Fatalf("returned space with pruned cache must be omitted, got %v", omit)
	}
}

// TestChatOwnersFilter: a chat object matches by `any.type`, and a
// DECLARING root by its id — a definition hosts its own datasets, so
// the general chat (its own type) is a chat too. A plain object and an
// unrelated definition match neither.
func TestChatOwnersFilter(t *testing.T) {
	f := chatOwnersFilter([]string{"chatType", "generalRoot"})
	a := &anyenc.Arena{}
	row := func(id, typ string, collections ...string) *anyenc.Value {
		v := a.NewObject()
		v.Set("id", a.NewString(id))
		anyNs := a.NewObject()
		if typ != "" {
			anyNs.Set("type", a.NewString(typ))
		}
		if len(collections) > 0 {
			arr := a.NewArray()
			for i, c := range collections {
				arr.SetArrayItem(i, a.NewString(c))
			}
			anyNs.Set("collections", arr)
		}
		v.Set("any", anyNs)
		return v
	}
	for name, tc := range map[string]struct {
		row  *anyenc.Value
		want bool
	}{
		"object of a chat type": {row("c1", "chatType"), true},
		"the declaring root":    {row("generalRoot", "__type__"), true},
		"plain object":          {row("o1", ""), false},
		"another definition":    {row("otherType", "__type__"), false},
		"only filed under it":   {row("o2", "page", "chatType"), false},
	} {
		if got := f.Ok(tc.row, nil); got != tc.want {
			t.Errorf("%s: Ok = %v, want %v", name, got, tc.want)
		}
	}
}
