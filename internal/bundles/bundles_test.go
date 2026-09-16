package bundles

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"
)

// fakeSpace satisfies space.Space for the resolver's needs: Resolve
// touches only Id() and Bundles(). The embedded interface is nil — any
// other method panics, which is the point: it pins what Resolve is
// allowed to use.
type fakeSpace struct {
	space.Space
	id      string
	bundles *fakeBundles
	status  *fakeSyncStatus
	objects *fakeObjects
	// role and indexErr drive the install gate: what this account may
	// do, and whether the registry converged.
	role     space.Permission
	indexErr error
	// waited records the deadline the gate gave WaitIndexSynced;
	// waits counts the calls.
	waited time.Duration
	waits  int
	// types serves the root's part declarations for the adopt-side
	// settled check; nil = every root reads as declaration-less.
	types *fakeTypes
	// collections serves the handle read the settled check runs for a
	// collection-declaring install.
	collections *fakeCollections
}

// fakeTypes stubs the two TypesAPI reads Ensure performs.
type fakeTypes struct {
	space.TypesAPI
	defs  map[string][]space.PartDef
	props map[string][]space.PropertyDef
	err   error
}

func (f *fakeTypes) Parts(_ context.Context, typeId string) ([]space.PartDef, error) {
	if f == nil {
		return nil, nil
	}
	return f.defs[typeId], f.err
}

func (f *fakeTypes) Properties(_ context.Context, typeId string) ([]space.PropertyDef, error) {
	if f == nil {
		return nil, nil
	}
	return f.props[typeId], f.err
}

// fakeCollections stubs the one CollectionsAPI read Ensure performs for
// a collection-declaring install.
type fakeCollections struct {
	space.CollectionsAPI
	info map[string]space.CollectionInfo
	err  error
}

func (f *fakeCollections) Get(_ context.Context, collectionId string) (space.CollectionInfo, error) {
	if f == nil {
		return space.CollectionInfo{}, nil
	}
	return f.info[collectionId], f.err
}

func (f *fakeSpace) Id() string                { return f.id }
func (f *fakeSpace) Types() space.TypesAPI     { return f.types }
func (f *fakeSpace) Bundles() space.BundlesAPI { return f.bundles }

func (f *fakeSpace) Collections() space.CollectionsAPI { return f.collections }

func (f *fakeSpace) SyncStatus() space.SyncStatusAPI {
	if f.status == nil {
		f.status = &fakeSyncStatus{}
	}
	return f.status
}

func (f *fakeSpace) Info() space.SpaceInfo { return space.SpaceInfo{Id: f.id, OwnRole: f.role} }
func (f *fakeSpace) WaitIndexSynced(ctx context.Context) error {
	f.waits++
	if dl, ok := ctx.Deadline(); ok {
		f.waited = time.Until(dl)
	}
	return f.indexErr
}
func (f *fakeSpace) Objects() space.ObjectService { return f.objects }

// fakeObjects records what the resolver asked to create or derive and
// satisfies the root-locality probe: absent stands in for a root
// whose tree has not reached this device.
type fakeObjects struct {
	space.ObjectService
	created int
	derived []space.DeriveObjectOpts
	absent  bool
}

func (f *fakeObjects) Get(context.Context, string) (*anyenc.Value, error) {
	if f.absent {
		return nil, space.ErrNotFound
	}
	return (&anyenc.Arena{}).NewObject(), nil
}

func (f *fakeObjects) Create(context.Context, space.CreateObjectOpts) (string, error) {
	f.created++
	return "created-root", nil
}

func (f *fakeObjects) Derive(_ context.Context, opts space.DeriveObjectOpts) (string, error) {
	f.derived = append(f.derived, opts)
	return "child-of-" + opts.ParentId + string(opts.Seed), nil
}

// fakeSyncStatus reports one fixed per-object state and a rollup whose
// peer counts drive the convergence wait's length.
type fakeSyncStatus struct {
	space.SyncStatusAPI
	state space.SyncState
	peers int
}

func (f *fakeSyncStatus) Object(objectId string) space.ObjectSyncStatus {
	return space.ObjectSyncStatus{ObjectId: objectId, State: f.state}
}

func (f *fakeSyncStatus) Space() space.SpaceSyncStatus {
	return space.SpaceSyncStatus{State: f.state, NetworkPeers: f.peers}
}

// fakeBundles records the roots ResolveLoser was asked to delete and
// can fail a chosen one, standing in for a loser whose tree has not
// synced to this device.
type fakeBundles struct {
	space.BundlesAPI
	mu      sync.Mutex
	row     space.Bundle
	getErr  error
	deleted []string
	failOn  map[string]error
	calls   int
	ensured []space.EnsureBundleRequest
	options []space.EnsureOptions
	// after runs at the end of Ensure, standing in for the side
	// effects the SDK's install has on local state.
	after func()
}

// Ensure records the request and registers a row for it, standing in
// for the SDK's adopt-or-install.
func (f *fakeBundles) Ensure(ctx context.Context, req space.EnsureBundleRequest, opts ...space.EnsureOption) (space.Bundle, bool, error) {
	f.mu.Lock()
	f.ensured = append(f.ensured, req)
	f.options = append(f.options, space.ApplyEnsureOptions(opts...))
	registered := f.row.RootId == ""
	f.mu.Unlock()
	rootId := "derived-root"
	if !req.DerivedRoot {
		if req.NewRoot == nil {
			// SDK-minted created root (datasets-carrying installs).
			rootId = "minted-root"
		} else {
			var err error
			if rootId, err = req.NewRoot(ctx); err != nil {
				return space.Bundle{}, false, err
			}
		}
	}
	// The SDK materializes (and stamps) a derived root on the adopt
	// path too, which is what makes it locally writable.
	if f.after != nil {
		f.after()
	}
	return space.Bundle{
		Id: req.Id, Name: req.Name, RootId: rootId,
		Roots: []string{rootId}, Derived: req.DerivedRoot,
	}, registered, nil
}

func (f *fakeBundles) Get(context.Context, string) (space.Bundle, error) {
	if f.getErr != nil {
		return space.Bundle{}, f.getErr
	}
	return f.row, nil
}

func (f *fakeBundles) List(context.Context) ([]space.Bundle, error) {
	return []space.Bundle{f.row}, nil
}

func (f *fakeBundles) ResolveLoser(_ context.Context, _, loserRootId string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := f.failOn[loserRootId]; err != nil {
		return err
	}
	f.deleted = append(f.deleted, loserRootId)
	return nil
}

// row carries one winner and one live loser — the shape every timing
// test needs before it reaches the guard.

func newFake() (*fakeSpace, *fakeBundles) {
	fb := &fakeBundles{
		row:    space.Bundle{Id: "test/v1", RootId: "w", Roots: []string{"w", "l1"}, Losers: []string{"l1"}},
		failOn: map[string]error{},
	}
	return &fakeSpace{id: "space1", bundles: fb}, fb
}

// newTestResolver removes the sync-state gate, which cannot be
// exercised without a live SDK.
func newTestResolver(grace time.Duration) *Resolver {
	r := NewResolver(grace)
	r.RetryDelay = time.Millisecond
	r.Quiescent = func(space.Space, string) bool { return true }
	return r
}

// TestResolveDeletesQuietLoser pins the happy path: a settled loser is
// handed straight to the SDK's cascade delete.
func TestResolveDeletesQuietLoser(t *testing.T) {
	sp, fb := newFake()
	if err := newTestResolver(0).Resolve(context.Background(), sp, "test/v1", "l1"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(fb.deleted) != 1 || fb.deleted[0] != "l1" {
		t.Fatalf("deleted = %v, want [l1]", fb.deleted)
	}
}

// TestResolveHoldsUntilQuiescent pins both timing guards. A client can
// only have merged what has arrived, so a loser inside the grace
// window or still syncing is refused — never deleted on a half-arrived
// tree.
func TestResolveHoldsUntilQuiescent(t *testing.T) {
	sp, fb := newFake()

	err := newTestResolver(time.Hour).Resolve(context.Background(), sp, "test/v1", "l1")
	if !errors.Is(err, ErrLoserNotReady) {
		t.Fatalf("grace window: err = %v, want ErrLoserNotReady", err)
	}

	r := newTestResolver(0)
	r.Quiescent = func(space.Space, string) bool { return false }
	err = r.Resolve(context.Background(), sp, "test/v1", "l1")
	if !errors.Is(err, ErrLoserNotReady) {
		t.Fatalf("syncing loser: err = %v, want ErrLoserNotReady", err)
	}

	if len(fb.deleted) != 0 || fb.calls != 0 {
		t.Fatalf("deletion attempted before the loser settled: %v", fb.deleted)
	}
}

// TestResolvePropagatesSDKRefusal pins that an undeletable loser (its
// tree has not synced here) surfaces as an error for the caller to
// retry, rather than being swallowed.
func TestResolvePropagatesSDKRefusal(t *testing.T) {
	sp, fb := newFake()
	fb.failOn["l1"] = errors.New("tree not synced")

	if err := newTestResolver(0).Resolve(context.Background(), sp, "test/v1", "l1"); err == nil {
		t.Fatal("undeletable loser reported no error")
	}
	if len(fb.deleted) != 0 {
		t.Fatalf("deleted = %v, want none", fb.deleted)
	}
}

// TestResolveRetryStopsOnSuccess pins that the background loop exits
// once the loser is gone instead of sleeping out its schedule.
func TestResolveRetryStopsOnSuccess(t *testing.T) {
	sp, fb := newFake()
	r := newTestResolver(0)

	done := make(chan struct{})
	go func() {
		r.ResolveRetry(context.Background(), sp, "test/v1", "l1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ResolveRetry did not return after the loser resolved")
	}
	if len(fb.deleted) != 1 {
		t.Fatalf("deleted = %v, want one loser", fb.deleted)
	}
}

// TestResolveRejectsNonLoser pins the permanent verdicts, which are
// reached before any timing guard: the winner is not a loser, and
// neither is a root nobody ever claimed.
func TestResolveRejectsNonLoser(t *testing.T) {
	sp, fb := newFake()
	r := newTestResolver(time.Hour) // grace would refuse first if consulted

	for _, target := range []string{"w", "never-claimed"} {
		err := r.Resolve(context.Background(), sp, "test/v1", target)
		if !errors.Is(err, space.ErrBundleNotLoser) {
			t.Fatalf("resolve %q: err = %v, want ErrBundleNotLoser", target, err)
		}
	}
	if fb.calls != 0 {
		t.Fatal("deletion attempted on a non-loser")
	}
}

// TestResolveIsIdempotent pins that a claimed root already gone from
// the live loser set resolves clean instead of erroring — the client
// may retry a call that already landed.
func TestResolveIsIdempotent(t *testing.T) {
	sp, fb := newFake()
	fb.row.Losers = nil

	if err := newTestResolver(0).Resolve(context.Background(), sp, "test/v1", "l1"); err != nil {
		t.Fatalf("resolve of an already-deleted loser: %v", err)
	}
	if fb.calls != 0 {
		t.Fatal("re-deleted an already-resolved loser")
	}
}

// TestResolveRetryStopsOnNotLoser pins that a target the registry says
// is not a loser ends the loop — retrying cannot change that verdict.
func TestResolveRetryStopsOnNotLoser(t *testing.T) {
	sp, fb := newFake()

	done := make(chan struct{})
	go func() {
		newTestResolver(0).ResolveRetry(context.Background(), sp, "test/v1", "w")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ResolveRetry kept retrying a non-loser")
	}
	if fb.calls != 0 {
		t.Fatalf("attempted deletion %d times on a non-loser", fb.calls)
	}
}

// TestResolveRetryHonorsCancellation pins that a cancelled context
// ends the loop instead of burning its whole backoff schedule.
func TestResolveRetryHonorsCancellation(t *testing.T) {
	sp, fb := newFake()
	fb.failOn["l1"] = errors.New("tree not synced")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		newTestResolver(0).ResolveRetry(ctx, sp, "test/v1", "l1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ResolveRetry ignored context cancellation")
	}
}

// TestResolveMapsSDKNotSynced pins that the SDK's "loser tree never
// arrived" verdict is the retryable one, not an opaque failure.
func TestResolveMapsSDKNotSynced(t *testing.T) {
	sp, fb := newFake()
	fb.failOn["l1"] = space.ErrLoserNotSynced

	err := newTestResolver(0).Resolve(context.Background(), sp, "test/v1", "l1")
	if !errors.Is(err, ErrLoserNotReady) {
		t.Fatalf("err = %v, want ErrLoserNotReady", err)
	}
}

// TestGraceRunsFromObservation pins that the window starts when the
// conflict became visible, not at the client's first resolve call: a
// loser listed long enough ago is resolvable on the first attempt.
func TestGraceRunsFromObservation(t *testing.T) {
	sp, fb := newFake()
	r := newTestResolver(time.Hour)

	if _, err := r.List(context.Background(), sp); err != nil {
		t.Fatalf("list: %v", err)
	}
	// Backdate the observation past the window — the same state a
	// client reaches after leaving the conflict on screen.
	r.mu.Lock()
	r.firstSeen[sp.Id()+"/l1"] = time.Now().Add(-2 * time.Hour)
	r.mu.Unlock()

	if err := r.Resolve(context.Background(), sp, "test/v1", "l1"); err != nil {
		t.Fatalf("resolve after the window elapsed: %v", err)
	}
	if len(fb.deleted) != 1 {
		t.Fatalf("deleted = %v, want [l1]", fb.deleted)
	}
	// A resolved loser stops being tracked.
	r.mu.Lock()
	_, tracked := r.firstSeen[sp.Id()+"/l1"]
	r.mu.Unlock()
	if tracked {
		t.Fatal("resolved loser still tracked")
	}
}

// TestResolveRetryDedups pins that a polling client cannot stack a
// retry loop per request.
func TestResolveRetryDedups(t *testing.T) {
	sp, fb := newFake()
	fb.failOn["l1"] = errors.New("tree not synced")
	r := newTestResolver(0)
	r.RetryDelay = 50 * time.Millisecond

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.ResolveRetry(context.Background(), sp, "test/v1", "l1")
		}()
	}
	wg.Wait()

	// One loop of retryAttempts, not ten.
	if fb.calls > retryAttempts {
		t.Fatalf("%d deletion attempts, want at most %d — retries stacked", fb.calls, retryAttempts)
	}
}

// TestResolveRetryStopsWhenUninstalled pins that a retry armed against
// a bundle that is not installed gives up instead of running for its
// whole schedule — and never deletes anything.
func TestResolveRetryStopsWhenUninstalled(t *testing.T) {
	sp, fb := newFake()
	fb.getErr = space.ErrBundleUnknown

	done := make(chan struct{})
	go func() {
		newTestResolver(0).ResolveRetry(context.Background(), sp, "test/v1", "l1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ResolveRetry kept retrying an uninstalled bundle")
	}
	if fb.calls != 0 {
		t.Fatalf("attempted %d deletions on an uninstalled bundle", fb.calls)
	}
}

// TestSyncQuiescentRequiresSynced pins the default probe: only an
// explicit synced verdict counts. Unknown is the state of a tree that
// has never produced a status hook — after a restart, or one that
// never arrived — and treating it as settled would license deleting a
// loser nobody here has seen.
func TestSyncQuiescentRequiresSynced(t *testing.T) {
	for state, want := range map[space.SyncState]bool{
		space.SyncStateSynced:  true,
		space.SyncStateUnknown: false,
		space.SyncStateSyncing: false,
		space.SyncStateOffline: false,
		space.SyncStateError:   false,
	} {
		sp := &fakeSpace{id: "space1", status: &fakeSyncStatus{state: state}}
		if got := syncQuiescent(sp, "l1"); got != want {
			t.Fatalf("state %v: quiescent = %v, want %v", state, got, want)
		}
	}
}

// newInstallFake is a space with nothing installed, so Ensure takes
// the install path. role is what this account may do in the space,
// indexErr whether its registry converged.
func newInstallFake(role space.Permission, indexErr error) *fakeSpace {
	return &fakeSpace{
		id:       "space1",
		bundles:  &fakeBundles{getErr: space.ErrBundleUnknown, failOn: map[string]error{}},
		objects:  &fakeObjects{},
		role:     role,
		indexErr: indexErr,
	}
}

// TestEnsureRefusesUnconvergedMember pins the fork guard for created
// roots: a member who cannot converge the registry cannot know whether
// someone already installed, and a blind create would mint a permanent
// second root.
func TestEnsureRefusesUnconvergedMember(t *testing.T) {
	sp := newInstallFake(space.PermissionWriter, errors.New("index wait expired"))
	ctx := context.Background()

	_, _, err := newTestResolver(0).Ensure(ctx, ctx, sp, Install{Id: "general-chat/v1"})
	if !errors.Is(err, ErrRegistryNotSynced) {
		t.Fatalf("err = %v, want ErrRegistryNotSynced", err)
	}
	if sp.objects.created != 0 {
		t.Fatalf("refused install still created %d root(s)", sp.objects.created)
	}
}

// TestEnsureOwnerInstallsUnconverged pins the owner escape: only this
// account's own devices could have competed, and the registry
// converges those.
func TestEnsureOwnerInstallsUnconverged(t *testing.T) {
	sp := newInstallFake(space.PermissionOwner, errors.New("index wait expired"))
	ctx := context.Background()

	b, installed, err := newTestResolver(0).Ensure(ctx, ctx, sp, Install{Id: "general-chat/v1"})
	if err != nil || !installed || b.RootId != "created-root" {
		t.Fatalf("owner install: b=%+v installed=%v err=%v", b, installed, err)
	}
}

// TestEnsureDerivedInstallsUnconverged is the offline-1-1 regression:
// both participants of a 1-1 are writers and neither can ever claim
// the owner escape, so a created install is refused forever while they
// are apart. A derived root has no competing id to mint, so it must
// install regardless of what the registry wait did.
func TestEnsureDerivedInstallsUnconverged(t *testing.T) {
	sp := newInstallFake(space.PermissionWriter, errors.New("index wait expired"))
	ctx := context.Background()

	b, installed, err := newTestResolver(0).Ensure(ctx, ctx, sp, Install{
		Id:              "general-chat/v1",
		Name:            "General",
		RootType:        "chat",
		RootCollections: []string{"miniapp"},
		RootProperties:  map[string]map[string]any{"any": {"description": "seeded"}},
		Derived:         true,
	})
	if err != nil {
		t.Fatalf("derived install refused: %v", err)
	}
	if !installed || !b.Derived || b.RootId == "" {
		t.Fatalf("derived install: b=%+v installed=%v", b, installed)
	}
	if len(sp.bundles.ensured) != 1 {
		t.Fatalf("ensure calls = %d, want 1", len(sp.bundles.ensured))
	}
	req := sp.bundles.ensured[0]
	if !req.DerivedRoot || req.NewRoot != nil {
		t.Fatalf("request did not ask the SDK for a derived root: %+v", req)
	}
	if req.RootType != "chat" || len(req.RootCollections) != 1 || req.RootCollections[0] != "miniapp" {
		t.Fatalf("root type/collections not forwarded: %q %+v", req.RootType, req.RootCollections)
	}
	if sp.objects.created != 0 {
		t.Fatalf("derived install created %d object(s)", sp.objects.created)
	}
	// Seeding is the SDK's, so it happens before the install is
	// registered — a failed seed must not leave a row to adopt.
	if req.RootProperties["any"]["description"] != "seeded" {
		t.Fatalf("root properties not forwarded to the SDK: %+v", req.RootProperties)
	}
}

// TestChildDerivationShape pins how a setup object binds to its root:
// by ParentId under a created root (so the cascade delete reaches it),
// by seed under a derived one (any-sync refuses a derived parent, and
// there is no cascade to preserve — the root is undeletable).
func TestChildDerivationShape(t *testing.T) {
	sp := newInstallFake(space.PermissionOwner, nil)
	ctx := context.Background()

	if _, err := Child(ctx, sp, space.Bundle{RootId: "root1"}, "memory/v1", "agent_memory"); err != nil {
		t.Fatalf("child of a created root: %v", err)
	}
	if _, err := Child(ctx, sp, space.Bundle{RootId: "root1", Derived: true}, "memory/v1", "agent_memory", "pinned"); err != nil {
		t.Fatalf("child of a derived root: %v", err)
	}
	if len(sp.objects.derived) != 2 {
		t.Fatalf("derive calls = %d, want 2", len(sp.objects.derived))
	}
	created, derived := sp.objects.derived[0], sp.objects.derived[1]
	if created.ParentId != "root1" || string(created.Seed) != "memory/v1" || created.Type != "agent_memory" {
		t.Fatalf("created root child must bind by parent: %+v", created)
	}
	if derived.ParentId != "" || string(derived.Seed) != "root1/memory/v1" {
		t.Fatalf("derived root child must bind by seed: %+v", derived)
	}
	// One type, the rest are collections.
	if derived.Type != "agent_memory" || len(derived.Collections) != 1 || derived.Collections[0] != "pinned" {
		t.Fatalf("child membership: type=%q collections=%+v", derived.Type, derived.Collections)
	}
}

// TestEnsureMintsAbsentDerivedRoot pins the one case where a winner
// that is not local is not a refusal: the registry row can arrive
// before the root tree does, and a derived root is this device's to
// mint. Refusing would make a client poll for a tree it could produce
// itself.
func TestEnsureMintsAbsentDerivedRoot(t *testing.T) {
	sp := newInstallFake(space.PermissionWriter, nil)
	sp.objects.absent = true
	sp.bundles.getErr = nil
	sp.bundles.row = space.Bundle{
		Id: "general-chat/v1", RootId: "derived-root",
		Roots: []string{"derived-root"}, Derived: true,
	}
	// The SDK's Ensure materializes the tree, so the locality probe
	// passes on the way out.
	sp.bundles.after = func() { sp.objects.absent = false }
	ctx := context.Background()

	b, installed, err := newTestResolver(0).Ensure(ctx, ctx, sp, Install{
		Id: "general-chat/v1", Derived: true,
	})
	if err != nil {
		t.Fatalf("absent derived winner refused: %v", err)
	}
	if b.RootId != "derived-root" || !b.Derived {
		t.Fatalf("bundle = %+v", b)
	}
	if installed {
		t.Fatal("materializing an existing install must not report installed")
	}
	if sp.objects.absent {
		t.Fatal("the adopt path must leave the root locally writable")
	}
	if len(sp.bundles.ensured) != 1 || !sp.bundles.ensured[0].DerivedRoot {
		t.Fatalf("did not reach the derived install path: %+v", sp.bundles.ensured)
	}
}

// TestEnsureRefusesAbsentCreatedRoot is the same shape for a created
// root, which this device cannot produce: its id would reject every
// write, so the caller is told to retry instead.
func TestEnsureRefusesAbsentCreatedRoot(t *testing.T) {
	sp := newInstallFake(space.PermissionOwner, nil)
	sp.objects.absent = true
	sp.bundles.getErr = nil
	sp.bundles.row = space.Bundle{
		Id: "general-chat/v1", RootId: "peer-root", Roots: []string{"peer-root"},
	}
	ctx := context.Background()

	_, _, err := newTestResolver(0).Ensure(ctx, ctx, sp, Install{Id: "general-chat/v1"})
	if !errors.Is(err, ErrRootNotLocal) {
		t.Fatalf("err = %v, want ErrRootNotLocal", err)
	}
	if len(sp.bundles.ensured) != 0 {
		t.Fatalf("refusal still installed: %+v", sp.bundles.ensured)
	}
}

// TestConvergeWaitTracksConnectivity pins how long the install gate
// holds the convergence wait open. Retrying a head-sync round against
// nobody answers the same way every time, so a device with no peer
// connected must not spend the full deadline learning that — while a
// connected one takes the full bound, since that wait is the only
// thing narrowing the window in which a derived claim can demote an
// install it has not seen.
func TestConvergeWaitTracksConnectivity(t *testing.T) {
	ctx := context.Background()
	inst := Install{Id: "general-chat/v1", Derived: true}

	r := newTestResolver(0)
	r.IndexWait = time.Hour
	r.OfflineIndexWait = time.Minute

	offline := newInstallFake(space.PermissionWriter, errors.New("index wait expired"))
	if _, _, err := r.Ensure(ctx, ctx, offline, inst); err != nil {
		t.Fatalf("offline derived install: %v", err)
	}
	if offline.waited > time.Minute {
		t.Fatalf("offline wait = %s, want the offline bound (%s)", offline.waited, time.Minute)
	}

	online := newInstallFake(space.PermissionWriter, errors.New("index wait expired"))
	online.status = &fakeSyncStatus{state: space.SyncStateSyncing, peers: 1}
	if _, _, err := r.Ensure(ctx, ctx, online, inst); err != nil {
		t.Fatalf("online derived install: %v", err)
	}
	if online.waited <= time.Minute {
		t.Fatalf("connected wait = %s, want the full bound (%s)", online.waited, time.Hour)
	}
}

// TestEnsurePartsAdoptStaysRead pins the reader-side contract for
// parts-carrying re-ensures: once the installed root carries its
// declaration (first-write-pinned), adoption is a pure read and must
// not reach the SDK's Ensure — its write gate would reject readers and
// guests re-running the documented idempotent request. A root without
// a declaration still falls through so the SDK can declare.
func TestEnsurePartsAdoptStaysRead(t *testing.T) {
	ctx := context.Background()
	inst := Install{Id: "notes/v1", Derived: true,
		Parts: []space.PartDraft{{Key: "entries", Datasets: []space.DatasetDraft{{Key: "entries"}}}}}

	sp := newInstallFake(space.PermissionReader, nil)
	sp.bundles.getErr = nil
	sp.bundles.row = space.Bundle{Id: "notes/v1", RootId: "root-1", Roots: []string{"root-1"}, Derived: true}
	sp.types = &fakeTypes{defs: map[string][]space.PartDef{"root-1": {{Key: "entries"}}}}
	b, installed, err := newTestResolver(0).Ensure(ctx, ctx, sp, inst)
	if err != nil || installed || b.RootId != "root-1" {
		t.Fatalf("reader adopt: b=%+v installed=%v err=%v", b, installed, err)
	}
	if len(sp.bundles.ensured) != 0 {
		t.Fatalf("declared root adopt reached SDK Ensure %d time(s)", len(sp.bundles.ensured))
	}

	sp2 := newInstallFake(space.PermissionOwner, nil)
	sp2.bundles.getErr = nil
	sp2.bundles.row = space.Bundle{Id: "notes/v1", RootId: "root-1", Roots: []string{"root-1"}, Derived: true}
	if _, _, err := newTestResolver(0).Ensure(ctx, ctx, sp2, inst); err != nil {
		t.Fatalf("undeclared root ensure: %v", err)
	}
	if len(sp2.bundles.ensured) != 1 {
		t.Fatalf("undeclared root must reach SDK Ensure once, got %d", len(sp2.bundles.ensured))
	}
}

// TestEnsureCreatedWithParts pins the SDK-minted created-root path:
// a parts-carrying non-derived install passes no NewRoot (Ensure
// mints and self-types the root — the only create the tech space
// allows) and reports installed from the SDK's registered bool.
func TestEnsureCreatedWithParts(t *testing.T) {
	sp := newInstallFake(space.PermissionOwner, nil)
	ctx := context.Background()
	b, installed, err := newTestResolver(0).Ensure(ctx, ctx, sp, Install{
		Id: "favorites/v1", Name: "Favorites",
		Parts: []space.PartDraft{{Key: "entries", Datasets: []space.DatasetDraft{{Key: "entries"}}}},
	})
	if err != nil || !installed || b.RootId != "minted-root" {
		t.Fatalf("created+datasets install: b=%+v installed=%v err=%v", b, installed, err)
	}
	if len(sp.bundles.ensured) != 1 {
		t.Fatalf("ensure calls = %d", len(sp.bundles.ensured))
	}
	req := sp.bundles.ensured[0]
	if req.DerivedRoot || req.NewRoot != nil || len(req.Parts) != 1 {
		t.Fatalf("request shape: %+v", req)
	}
	if sp.objects.created != 0 {
		t.Fatalf("resolver must not create the root itself, got %d", sp.objects.created)
	}
}

// TestEnsurePartsAdoptRoles pins the settled verdicts around the
// heal fallthrough: a defs-read error or a read-only role always
// adopts (never the SDK write gate); only a writer with a
// declaration-less root falls through so the SDK heals.
func TestEnsurePartsAdoptRoles(t *testing.T) {
	ctx := context.Background()
	inst := Install{Id: "notes/v1", Derived: true,
		Parts: []space.PartDraft{{Key: "entries", Datasets: []space.DatasetDraft{{Key: "entries"}}}}}
	row := space.Bundle{Id: "notes/v1", RootId: "root-1", Roots: []string{"root-1"}, Derived: true}

	// Reader + defs-read error: adopt.
	sp := newInstallFake(space.PermissionReader, nil)
	sp.bundles.getErr = nil
	sp.bundles.row = row
	sp.types = &fakeTypes{err: errors.New("store closing")}
	if _, _, err := newTestResolver(0).Ensure(ctx, ctx, sp, inst); err != nil {
		t.Fatalf("reader adopt on read error: %v", err)
	}
	if len(sp.bundles.ensured) != 0 {
		t.Fatalf("read error pushed the reader into SDK Ensure")
	}

	// Reader + empty defs: adopt (cannot heal).
	sp2 := newInstallFake(space.PermissionReader, nil)
	sp2.bundles.getErr = nil
	sp2.bundles.row = row
	sp2.types = &fakeTypes{}
	if _, _, err := newTestResolver(0).Ensure(ctx, ctx, sp2, inst); err != nil {
		t.Fatalf("reader adopt on empty defs: %v", err)
	}
	if len(sp2.bundles.ensured) != 0 {
		t.Fatalf("declaration-less adopt pushed the reader into SDK Ensure")
	}
}

// TestEnsurePropertiesAdoptRoles pins the settled verdict for a
// properties-declaring install: a root carrying every handle adopts
// as a pure read; a writer with a missing handle falls through so the
// SDK heals; a reader with a missing handle adopts (it cannot heal and
// must never hit the write gate).
func TestEnsurePropertiesAdoptRoles(t *testing.T) {
	ctx := context.Background()
	inst := Install{Id: "wiki/v1", Derived: true,
		Properties: []space.PropertyDraft{{XKey: "parentId", Kind: space.PropertyKindString}, {XKey: "pos", Kind: space.PropertyKindString}}}
	row := space.Bundle{Id: "wiki/v1", RootId: "root-1", Roots: []string{"root-1"}, Derived: true}
	both := map[string][]space.PropertyDef{"root-1": {{Id: "p1", XKey: "parentId"}, {Id: "p2", XKey: "pos"}}}
	one := map[string][]space.PropertyDef{"root-1": {{Id: "p1", XKey: "parentId"}}}

	// Every handle present: adopt, no SDK call, even for a writer.
	sp := newInstallFake(space.PermissionWriter, nil)
	sp.bundles.getErr = nil
	sp.bundles.row = row
	sp.types = &fakeTypes{props: both}
	b, installed, err := newTestResolver(0).Ensure(ctx, ctx, sp, inst)
	if err != nil || installed || b.RootId != "root-1" {
		t.Fatalf("settled adopt: b=%+v installed=%v err=%v", b, installed, err)
	}
	if len(sp.bundles.ensured) != 0 {
		t.Fatalf("settled properties adopt reached SDK Ensure %d time(s)", len(sp.bundles.ensured))
	}

	// A writer with a missing handle falls through so the SDK heals it.
	sp2 := newInstallFake(space.PermissionWriter, nil)
	sp2.bundles.getErr = nil
	sp2.bundles.row = row
	sp2.types = &fakeTypes{props: one}
	if _, _, err := newTestResolver(0).Ensure(ctx, ctx, sp2, inst); err != nil {
		t.Fatalf("writer heal: %v", err)
	}
	if len(sp2.bundles.ensured) != 1 || len(sp2.bundles.ensured[0].Properties) != 2 {
		t.Fatalf("writer with a missing handle must reach SDK Ensure once with the declaration, got %+v", sp2.bundles.ensured)
	}
	if sp2.bundles.options[0].SystemInstall {
		t.Fatal("a client install must not carry the system-install option")
	}

	// A reader with a missing handle adopts.
	sp3 := newInstallFake(space.PermissionReader, nil)
	sp3.bundles.getErr = nil
	sp3.bundles.row = row
	sp3.types = &fakeTypes{props: one}
	if _, _, err := newTestResolver(0).Ensure(ctx, ctx, sp3, inst); err != nil {
		t.Fatalf("reader adopt on a missing handle: %v", err)
	}
	if len(sp3.bundles.ensured) != 0 {
		t.Fatalf("missing handle pushed the reader into SDK Ensure")
	}

	// The server's own install carries the option through.
	sp4 := newInstallFake(space.PermissionOwner, nil)
	sys := inst
	sys.SystemInstall = true
	if _, _, err := newTestResolver(0).Ensure(ctx, ctx, sp4, sys); err != nil {
		t.Fatalf("system install: %v", err)
	}
	if len(sp4.bundles.options) != 1 || !sp4.bundles.options[0].SystemInstall {
		t.Fatalf("system install must reach the SDK as the ensure option, got %+v", sp4.bundles.options)
	}
}

// TestEnsureCollectionDeclaration pins the collection form: the flag
// reaches the SDK request, the settled check reads the handle off the
// collections surface (not the types one), and a root that already
// carries it adopts as a pure read.
func TestEnsureCollectionDeclaration(t *testing.T) {
	ctx := context.Background()
	inst := Install{Id: "wiki/v1", Name: "Wiki", XKey: "wiki", Collection: true,
		Properties: []space.PropertyDraft{{XKey: "parentId", Kind: space.PropertyKindString}}}
	row := space.Bundle{Id: "wiki/v1", RootId: "root-1", Roots: []string{"root-1"}}

	// Handle present: adopt, no SDK call, no types read.
	sp := newInstallFake(space.PermissionWriter, nil)
	sp.bundles.getErr = nil
	sp.bundles.row = row
	sp.types = &fakeTypes{props: map[string][]space.PropertyDef{"root-1": {{Id: "p1", XKey: "parentId"}}}}
	sp.collections = &fakeCollections{info: map[string]space.CollectionInfo{"root-1": {Id: "root-1", XKey: "wiki"}}}
	b, installed, err := newTestResolver(0).Ensure(ctx, ctx, sp, inst)
	if err != nil || installed || b.RootId != "root-1" {
		t.Fatalf("settled collection adopt: b=%+v installed=%v err=%v", b, installed, err)
	}
	if len(sp.bundles.ensured) != 0 {
		t.Fatalf("settled collection adopt reached SDK Ensure %d time(s)", len(sp.bundles.ensured))
	}

	// Handle missing: a writer falls through and the SDK heals it, with
	// Collection set so the root takes the collection marker.
	sp2 := newInstallFake(space.PermissionWriter, nil)
	sp2.bundles.getErr = nil
	sp2.bundles.row = row
	sp2.collections = &fakeCollections{}
	if _, _, err := newTestResolver(0).Ensure(ctx, ctx, sp2, inst); err != nil {
		t.Fatalf("writer heal: %v", err)
	}
	if len(sp2.bundles.ensured) != 1 || !sp2.bundles.ensured[0].Collection {
		t.Fatalf("collection flag not forwarded: %+v", sp2.bundles.ensured)
	}
	// A declaring root is minted by the SDK, never through Objects.Create.
	if sp2.bundles.ensured[0].NewRoot != nil || sp2.objects.created != 0 {
		t.Fatalf("collection install minted its own root: %+v created=%d", sp2.bundles.ensured[0], sp2.objects.created)
	}
	if !inst.DeclaresCollection() || inst.DeclaresType() {
		t.Fatalf("DeclaresCollection/DeclaresType on %+v", inst)
	}
}

// TestSetupWaitsOnce pins the reason Setup exists: a walk over several
// installs consults the registry-convergence wait once and reuses the
// verdict, runs the caller's pre-install rule once per genuine
// install, and stops at the entry the rule refuses, naming it.
func TestSetupWaitsOnce(t *testing.T) {
	sp := newInstallFake(space.PermissionOwner, errors.New("index wait expired"))
	ctx := context.Background()
	installs := []Install{{Id: "a/v1"}, {Id: "b/v1"}, {Id: "c/v1"}}

	var seen []string
	results, err := newTestResolver(0).Setup(ctx, ctx, sp, installs,
		func(_ context.Context, _ space.Space, inst Install) error {
			seen = append(seen, inst.Id)
			return nil
		})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if len(results) != 3 || sp.objects.created != 3 {
		t.Fatalf("results=%d created=%d", len(results), sp.objects.created)
	}
	if sp.waits != 1 {
		t.Fatalf("WaitIndexSynced called %d times, want once", sp.waits)
	}
	if strings.Join(seen, ",") != "a/v1,b/v1,c/v1" {
		t.Fatalf("pre-install hook saw %v", seen)
	}

	// A refusal by the hook stops the walk at that entry; the results
	// before it stand and the error names the install.
	sp = newInstallFake(space.PermissionOwner, nil)
	refuse := errors.New("handle taken")
	results, err = newTestResolver(0).Setup(ctx, ctx, sp, installs,
		func(_ context.Context, _ space.Space, inst Install) error {
			if inst.Id == "b/v1" {
				return refuse
			}
			return nil
		})
	var se *SetupError
	if !errors.As(err, &se) || se.Install.Id != "b/v1" || !errors.Is(err, refuse) {
		t.Fatalf("err = %v, want SetupError on b/v1", err)
	}
	if len(results) != 1 || results[0].Install.Id != "a/v1" || sp.objects.created != 1 {
		t.Fatalf("partial results = %+v created=%d", results, sp.objects.created)
	}
}
