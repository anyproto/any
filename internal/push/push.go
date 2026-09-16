// Package push runs the account's client side of push notifications:
// device-token persistence + registration with the push
// node, the subscription sync loop (desired-topic reconcile →
// SubscribeAll full replace), and a buffered notify queue so HTTP
// handlers never block on the push server. Structural twin of
// internal/indexer — per-account, SDK-consuming, background-looping;
// constructed by the engine only when config.Push.Active().
//
// The SDK owns crypto + transport (space.PushAPI); this package owns
// the account policy: WHICH topics (space-level and per-chat modes,
// topics.go), WHEN to re-sync, WHAT survives a restart (the token
// file), and the chat notify hooks the HTTP handlers call after
// send/edit/read (chatpush.go — heart-interoperable payloads).
package push

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"
	"github.com/anyproto/any-sync/app/logger"
	"github.com/anyproto/anytype-push-server/pushclient/pushapi"
	"go.uber.org/zap"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/chat"
)

const (
	// syncEvery is the periodic re-sync tick (heart parity: token
	// registration + subscription re-sync every 5 minutes). The dirty
	// channel covers the reactive path; the tick is the retry/catch-up.
	syncEvery = 5 * time.Minute
	// syncDebounce coalesces bursts of space-list events into one
	// reconcile round.
	syncDebounce = 250 * time.Millisecond
	// syncRoundBudget bounds one reconcile round so a wedged RPC can't
	// stall the loop past the next tick.
	syncRoundBudget = time.Minute
	// forwardBudget bounds the synchronous forward inside SetToken /
	// RevokeToken so HTTP latency stays bounded when the push node is
	// slow; a timed-out set falls back to the background retry.
	forwardBudget = 5 * time.Second

	// notifyAttempts / notifyRetryDelay mirror heart's notify retry
	// loop: 6 attempts, 10s apart, break early on ErrNoValidTopics.
	notifyAttempts   = 6
	notifyRetryDelay = 10 * time.Second
	// notifyQueueCap bounds the async delivery queue; overflow drops
	// the notification (push is best-effort by design).
	notifyQueueCap = 256
)

// notifyJob is one queued (encrypted-on-send) notification.
type notifyJob struct {
	spaceId string
	topics  []string
	payload []byte
	groupId string
	silent  bool
}

// Service is the per-account push service. New → Start → Close; all
// exported methods are safe for concurrent use. The chat handler
// hooks (NotifyChatMessage / NotifyChatEdit / NotifyChatRead in
// chatpush.go) feed Enqueue.
type Service struct {
	sdk *anysyncsdk.SDK
	dir string
	lg  logger.CtxLogger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// dirty coalesces sync-loop kicks (cap 1, non-blocking sends) —
	// the space-list subscribe callback runs on the SDK's dispatcher
	// goroutine and must not block. Lost signals are harmless: the
	// reconcile is state-driven, the next kick or tick catches up.
	dirty   chan struct{}
	notifyQ chan notifyJob

	mu             sync.Mutex
	token          *deviceToken // nil = no token on this device
	tokenForwarded bool         // token accepted by the push node since it last changed
	lastSyncedHash string       // desiredHash of the last SUCCESSFUL SubscribeAll round ("" = never)
	notConfigured  bool         // SDK opened without a push node — loops idle (logged once)
	capture        CaptureFunc  // test seam — diverts Enqueue, nil in production
	// lastChatModes is the per-space last-known-good chat enumeration —
	// the fail-safe collectChatModes falls back to when a space's
	// enumeration fails (a bulk fallback would silently unmute muted
	// chats). Keyed by spaceId; a present-but-empty entry means "known
	// good: no chats". Pruned to the active space set every round.
	lastChatModes map[string][]chatNotify

	// Test seams — set before Start, nil in production. pushAPI
	// overrides s.sdk.Push() (fake push node); chatEnum overrides the
	// SDK-backed per-space chat enumeration in collectChatModes.
	pushAPI  space.PushAPI
	chatEnum func(ctx context.Context, spaceId string) ([]chatNotify, error)

	cancelSpaceSub func()
}

// push returns the push API — the SDK's, unless a test injected one.
func (s *Service) push() space.PushAPI {
	if s.pushAPI != nil {
		return s.pushAPI
	}
	return s.sdk.Push()
}

// CaptureFunc receives one would-be-enqueued notification. Test seam;
// see CaptureNotifications.
type CaptureFunc func(spaceId string, topics []string, payload []byte, groupId string, silent bool)

// CaptureNotifications diverts every subsequent Enqueue into fn
// instead of the delivery queue — the test seam for asserting the
// chat-hook outputs (topics / payload / groupId) without a reachable
// push node. Pass nil to restore normal delivery.
func (s *Service) CaptureNotifications(fn CaptureFunc) {
	s.mu.Lock()
	s.capture = fn
	s.mu.Unlock()
}

// New constructs the service. accountDir is the per-account data dir
// (wallet.key, server.pid, …) where push-token.json lives. Call Start
// to begin background work; the token/notify surfaces are inert but
// safe before that.
func New(sdk *anysyncsdk.SDK, accountDir string) *Service {
	return &Service{
		sdk:     sdk,
		dir:     accountDir,
		lg:      logger.NewNamed("push"),
		dirty:   make(chan struct{}, 1),
		notifyQ: make(chan notifyJob, notifyQueueCap),
	}
}

func (s *Service) tokenPath() string { return filepath.Join(s.dir, tokenFileName) }

// Start loads the persisted device token, subscribes to space-list
// changes, and spawns the sync + notify loops. Non-blocking; a
// persisted token is re-registered by the first sync round in the
// background (never fails boot). The passed ctx bounds all background
// work — cancel it (or call Close) to stop.
func (s *Service) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)

	tok, err := loadTokenFile(s.tokenPath())
	if err != nil {
		s.lg.Warn("load push token", zap.Error(err))
	}
	s.mu.Lock()
	s.token = tok
	s.mu.Unlock()

	// Settings changes arrive as Updated rows, new/removed spaces as
	// Added/Removed — every event kind can change the desired set, so
	// each just kicks the (hash-gated) reconcile.
	s.cancelSpaceSub = s.sdk.Spaces().Subscribe(func(space.SpaceListEvent) {
		s.Kick()
	})

	s.wg.Add(2)
	go s.syncLoop()
	go s.notifyLoop()
	s.Kick() // initial reconcile (also re-registers a persisted token)
}

// Close stops the loops. Best-effort teardown: an in-flight RPC
// finishes or context-cancels; queued notifications are dropped.
// Idempotent.
func (s *Service) Close() error {
	if s.cancelSpaceSub != nil {
		s.cancelSpaceSub()
		s.cancelSpaceSub = nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return nil
}

// Kick schedules a subscription re-sync (non-blocking, coalesced).
// SetToken uses it so a fresh token gets its subscriptions
// immediately instead of waiting for the tick.
func (s *Service) Kick() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

// SetToken persists the device token and forwards it to the push
// node. The persist is the durable part; the forward is bounded by
// forwardBudget and, on transient failure, retried in the background
// by the sync loop — so a slow/unreachable push node never fails the
// call. Only ErrPushNotConfigured (and local disk errors) surface.
func (s *Service) SetToken(ctx context.Context, platform space.PushPlatform, token string) error {
	switch platform {
	case space.PushPlatformIOS, space.PushPlatformAndroid:
	default:
		return fmt.Errorf("push: unknown platform %q", platform)
	}
	if token == "" {
		return errors.New("push: empty token")
	}
	tok := deviceToken{Platform: string(platform), Token: token}
	if err := saveTokenFile(s.tokenPath(), tok); err != nil {
		return err
	}
	s.mu.Lock()
	s.token = &tok
	s.tokenForwarded = false
	s.mu.Unlock()

	fctx, cancel := context.WithTimeout(ctx, forwardBudget)
	defer cancel()
	if err := s.push().SetToken(fctx, platform, token); err != nil {
		if errors.Is(err, space.ErrPushNotConfigured) {
			// Terminal for this process — roll the persist back so a
			// rejected set leaves no half-registered state behind.
			_ = removeTokenFile(s.tokenPath())
			s.mu.Lock()
			s.token = nil
			s.mu.Unlock()
			return err
		}
		s.lg.Warn("push token forward failed — retrying in background", zap.Error(err))
	} else {
		s.markForwarded(tok)
	}
	s.Kick()
	return nil
}

// markForwarded records a successful token forward — but only when the
// device token is STILL the one that was actually sent. A concurrent
// SetToken landing between the snapshot and the RPC completing must
// not be masked by the stale forward: its own synchronous forward may
// have failed transiently, and marking forwarded here would stop the
// sync loop from ever re-forwarding the newer token. When the token
// moved, tokenForwarded stays false and the next round forwards the
// new value.
func (s *Service) markForwarded(sent deviceToken) {
	s.mu.Lock()
	if s.token != nil && s.token.Platform == sent.Platform && s.token.Token == sent.Token {
		s.tokenForwarded = true
	}
	s.mu.Unlock()
}

// RevokeToken forwards the revoke (best-effort — the token file is
// removed locally regardless, so a device that logged out stays
// logged out even if the push node was unreachable at that moment)
// and deletes the persisted file.
func (s *Service) RevokeToken(ctx context.Context) error {
	fctx, cancel := context.WithTimeout(ctx, forwardBudget)
	defer cancel()
	if err := s.push().RevokeToken(fctx); err != nil {
		if errors.Is(err, space.ErrPushNotConfigured) {
			return err
		}
		s.lg.Warn("push token revoke forward failed — token removed locally only", zap.Error(err))
	}
	if err := removeTokenFile(s.tokenPath()); err != nil {
		return err
	}
	s.mu.Lock()
	s.token = nil
	s.tokenForwarded = false
	s.mu.Unlock()
	return nil
}

// TokenStatus reports the LOCAL registration state — whether a token
// file exists on this device and for which platform. It does not
// round-trip the push node.
func (s *Service) TokenStatus() (registered bool, platform string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token == nil {
		return false, ""
	}
	return true, s.token.Platform
}

// Subscriptions returns the account's server-held topic set (raw
// {spaceKey, topic} rows, unsigned) — SDK passthrough.
func (s *Service) Subscriptions(ctx context.Context) ([]space.PushSubscription, error) {
	return s.push().Subscriptions(ctx)
}

// Enqueue schedules one notification for buffered async delivery
// (notifyAttempts × notifyRetryDelay, break early when the server
// reports no valid topics). silent routes through NotifySilent
// (own-devices wakeup; topics ignored). Never blocks: on queue
// overflow the notification is dropped with a warning — push is a
// best-effort side channel, the message itself is already synced.
// Fed by the chat handler hooks in chatpush.go.
func (s *Service) Enqueue(spaceId string, topics []string, payload []byte, groupId string, silent bool) {
	s.mu.Lock()
	capture := s.capture
	s.mu.Unlock()
	if capture != nil {
		capture(spaceId, topics, payload, groupId, silent)
		return
	}
	select {
	case s.notifyQ <- notifyJob{spaceId: spaceId, topics: topics, payload: payload, groupId: groupId, silent: silent}:
	default:
		s.lg.Warn("push notify queue full — dropping notification", zap.String("spaceId", spaceId))
	}
}

// --- sync loop ------------------------------------------------------

// syncLoop reconciles the account's server-side subscription state:
// wake on Kick (debounced/coalesced) or the periodic tick, then run
// one hash-gated reconcile round. Failed rounds don't advance the
// hash, so the next wake retries the same state.
func (s *Service) syncLoop() {
	defer s.wg.Done()
	t := time.NewTicker(syncEvery)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.dirty:
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(syncDebounce):
			}
			// Coalesce kicks that arrived during the debounce window.
			select {
			case <-s.dirty:
			default:
			}
		case <-t.C:
		}
		s.syncOnce()
	}
}

// syncOnce runs one reconcile round: ensure the persisted token is
// registered, rebuild the desired topic set from the space list, and
// — when it differs from the last successfully synced set —
// RegisterSpace the owned/1-1 spaces and SubscribeAll (full replace).
//
// A round can be PARTIAL: spaces whose chat enumeration failed with no
// last-known-good cache are omitted from the desired set (see
// collectChatModes). Partial rounds still SubscribeAll so healthy
// spaces converge, but never advance lastSyncedHash — the omitted
// space is retried on the next kick/tick.
func (s *Service) syncOnce() {
	s.mu.Lock()
	idle := s.notConfigured
	s.mu.Unlock()
	if idle {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, syncRoundBudget)
	defer cancel()

	s.ensureToken(ctx)

	infos, err := s.sdk.Spaces().List(ctx)
	if err != nil {
		s.lg.Warn("push sync: list spaces", zap.Error(err))
		return
	}
	chats, omit := s.collectChatModes(ctx, infos)
	partial := len(omit) > 0
	if partial {
		kept := make([]space.SpaceInfo, 0, len(infos))
		for _, info := range infos {
			if !omit[info.Id] {
				kept = append(kept, info)
			}
		}
		infos = kept
	}
	subs, register := desiredSubs(infos, chats, s.sdk.Account().Id())
	h := desiredHash(register, subs)
	s.mu.Lock()
	unchanged := h == s.lastSyncedHash
	s.mu.Unlock()
	if unchanged {
		// Nothing to change server-side. On a partial round the hash was
		// never advanced past this state, so the omitted space still
		// retries on the next kick/tick.
		return
	}

	if !s.reconcile(ctx, register, subs) {
		return
	}
	if partial {
		// The omitted space's desired state is deliberately incomplete —
		// leave lastSyncedHash untouched so the next kick/tick recomputes
		// and retries instead of settling here.
		s.lg.Debug("push subscriptions synced (partial round — omitted spaces retry)",
			zap.Int("spaces", len(subs)), zap.Int("omitted", len(omit)))
		return
	}
	s.mu.Lock()
	s.lastSyncedHash = h
	s.mu.Unlock()
	s.lg.Debug("push subscriptions synced",
		zap.Int("spaces", len(subs)), zap.Int("registered", len(register)))
}

// reconcile pushes one desired state to the push node: RegisterSpace
// for the owned/1-1 ids, then the SubscribeAll full replace. Reports
// whether the SubscribeAll landed (the caller only advances the hash
// on true).
//
// Per-space registration failures warn and CONTINUE — one
// unregistrable space must not starve the whole account's reconcile.
// The failed space keeps its topics in the SubscribeAll payload:
// registration is best-effort forward-compat (the push server's
// ExistedSpaces check is currently a no-op), and RegisterSpace is
// idempotent, so the next changed round retries it harmlessly.
// ErrPushNotConfigured still aborts the round and idles the loop; a
// dead round context aborts too (everything after it would fail the
// same way).
func (s *Service) reconcile(ctx context.Context, register []string, subs []space.PushSpaceTopics) bool {
	for _, id := range register {
		if err := s.push().RegisterSpace(ctx, id); err != nil {
			if errors.Is(err, space.ErrPushNotConfigured) || ctx.Err() != nil {
				s.syncFailure(err, "register space")
				return false
			}
			s.lg.Warn("push sync: register space failed — skipping it, reconcile continues",
				zap.String("spaceId", id), zap.Error(err))
		}
	}
	if err := s.push().SubscribeAll(ctx, subs); err != nil {
		s.syncFailure(err, "subscribe all")
		return false
	}
	return true
}

// chatOwners returns the types declaring the space's chat collection —
// the canonical chat_messages, shared by every type that names the
// chat module in a part. Empty when nothing declares it yet.
func chatOwners(sp space.Space) []string {
	for _, ds := range sp.Datasets() {
		if ds.Name == chat.Dataset {
			return ds.Owners
		}
	}
	return nil
}

// chatOwnersFilter matches objects rows whose type is one of the
// declaring types, and the declaring roots themselves (a definition
// object hosts its own datasets — the general chat is its own root).
func chatOwnersFilter(owners []string) query.Filter {
	a := &anyenc.Arena{}
	vals := make([]*anyenc.Value, len(owners))
	for i, o := range owners {
		vals[i] = a.NewString(o)
	}
	return query.Or{
		query.Key{Path: []string{"any", "type"}, Filter: query.NewInValue(vals...)},
		query.Key{Path: []string{"id"}, Filter: query.NewInValue(vals...)},
	}
}

// collectChatModes enumerates every ACTIVE space's chat objects and
// their raw `chat.notifyMode` property (read off the objects row,
// where the account-scoped value is mirrored) — the per-chat input to
// desiredSubs. Returns nil `out` entries for spaces that contribute
// nothing, plus the set of spaces to OMIT from this round's desired
// state.
//
// Failure policy — fail-SAFE, never fail-open. A space whose
// enumeration fails must NOT degrade to its space-level BULK topics:
// bulk `chats` would silently unmute every muted chat in the space (a
// mute violation, the worst outcome). Instead:
//   - every SUCCESSFUL enumeration is cached (lastChatModes, including
//     the empty result), and a failing space reuses its last-known-good
//     entries (warn);
//   - a failing space with NO cache is omitted from the desired set
//     entirely for this round and reported in `omit` — the caller runs
//     a partial round (SubscribeAll still happens so healthy spaces
//     converge, but lastSyncedHash does not advance, so the space is
//     retried next kick/tick). Omission loses at most one tick of that
//     space's pushes — strictly safer than violating a mute.
//
// Access pattern: Spaces().Get, the same handle the indexer uses for
// its per-space workers — for a StatusActive row that's (re)opening
// local storage, never a network join. Non-active rows are skipped
// before Get so a pending/deleted row is never force-materialized
// (desiredSubs ignores them anyway).
func (s *Service) collectChatModes(ctx context.Context, infos []space.SpaceInfo) (out map[string][]chatNotify, omit map[string]bool) {
	active := make(map[string]bool, len(infos))
	for _, info := range infos {
		if info.Status != space.StatusActive {
			continue
		}
		active[info.Id] = true
		entries, err := s.chatModes(ctx, info.Id)
		if err != nil {
			cached, ok := s.cachedChatModes(info.Id)
			if !ok {
				s.lg.Warn("push sync: chat-mode enumeration failed with no last-known-good cache — omitting space this round",
					zap.String("spaceId", info.Id), zap.Error(err))
				if omit == nil {
					omit = make(map[string]bool)
				}
				omit[info.Id] = true
				continue
			}
			s.lg.Warn("push sync: chat-mode enumeration failed — using last-known-good modes",
				zap.String("spaceId", info.Id), zap.Error(err))
			entries = cached
		} else {
			s.storeChatModes(info.Id, entries)
		}
		if len(entries) == 0 {
			continue
		}
		if out == nil {
			out = make(map[string][]chatNotify)
		}
		out[info.Id] = entries
	}
	s.pruneChatModes(active)
	return out, omit
}

// chatModes enumerates one space's chat objects. Routed through the
// chatEnum test seam when set.
func (s *Service) chatModes(ctx context.Context, spaceId string) ([]chatNotify, error) {
	if s.chatEnum != nil {
		return s.chatEnum(ctx, spaceId)
	}
	sp, err := s.sdk.Spaces().Get(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	// Sorted by id so the topic order — and therefore desiredHash —
	// is deterministic across rounds.
	owners := chatOwners(sp)
	if len(owners) == 0 {
		return nil, nil
	}
	rows, err := sp.QueryObjects().Filter(chatOwnersFilter(owners)).Sort("id").All(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]chatNotify, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, chatNotify{
			objectId: string(row.GetStringBytes("id")),
			mode:     string(row.GetStringBytes(chat.Module, chat.PropNotifyMode)),
		})
	}
	return entries, nil
}

// cachedChatModes returns a space's last-known-good enumeration. ok
// distinguishes "cached as empty" (a successful zero-chat round —
// bulk topics are correct) from "never enumerated successfully".
func (s *Service) cachedChatModes(spaceId string) ([]chatNotify, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, ok := s.lastChatModes[spaceId]
	return entries, ok
}

// storeChatModes records a successful enumeration (including empty).
func (s *Service) storeChatModes(spaceId string, entries []chatNotify) {
	s.mu.Lock()
	if s.lastChatModes == nil {
		s.lastChatModes = make(map[string][]chatNotify)
	}
	s.lastChatModes[spaceId] = entries
	s.mu.Unlock()
}

// pruneChatModes drops cache entries for spaces that left the active
// set (deleted / declined / removed) so the cache tracks the space
// list instead of growing without bound.
func (s *Service) pruneChatModes(active map[string]bool) {
	s.mu.Lock()
	for id := range s.lastChatModes {
		if !active[id] {
			delete(s.lastChatModes, id)
		}
	}
	s.mu.Unlock()
}

// ensureToken re-forwards the persisted device token when the push
// node hasn't acknowledged it yet (boot with an existing file, or a
// SetToken whose synchronous forward failed). Failures stay
// non-fatal: subscriptions are account-scoped and independent of the
// token, so the round continues.
//
// The forward runs outside the lock, so a concurrent SetToken can
// replace s.token mid-flight; markForwarded re-checks the snapshot so
// the stale forward can never mask the newer token (which would
// otherwise stay un-forwarded forever if its own synchronous forward
// failed).
func (s *Service) ensureToken(ctx context.Context) {
	s.mu.Lock()
	var snap deviceToken
	has := s.token != nil
	if has {
		snap = *s.token
	}
	done := s.tokenForwarded
	s.mu.Unlock()
	if !has || done {
		return
	}
	if err := s.push().SetToken(ctx, space.PushPlatform(snap.Platform), snap.Token); err != nil {
		s.syncFailure(err, "set token")
		return
	}
	s.markForwarded(snap)
}

// syncFailure classifies a sync-round error: ErrPushNotConfigured is
// terminal for the process (config is fixed at SDK open) — log once
// at info and idle the loop; anything else is transient — warn and
// let the next kick/tick retry.
func (s *Service) syncFailure(err error, op string) {
	if errors.Is(err, space.ErrPushNotConfigured) {
		s.mu.Lock()
		logged := s.notConfigured
		s.notConfigured = true
		s.mu.Unlock()
		if !logged {
			s.lg.Info("push node not configured in the SDK — push sync idles")
		}
		return
	}
	if s.ctx != nil && s.ctx.Err() != nil {
		return // shutdown, not a failure
	}
	s.lg.Warn("push sync: "+op, zap.Error(err))
}

// --- notify loop ----------------------------------------------------

func (s *Service) notifyLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case job := <-s.notifyQ:
			s.deliver(job)
		}
	}
}

// deliver pushes one job with heart's retry discipline: notifyAttempts
// tries, notifyRetryDelay apart. pushapi.ErrNoValidTopics ends the
// loop early — nobody is subscribed to any of the topics, which is a
// normal outcome, not a failure (the sentinel is errors.Is-able: the
// SDK transport wraps whole-RPC errors with %w after rpcerr.Unwrap).
func (s *Service) deliver(job notifyJob) {
	for attempt := 1; attempt <= notifyAttempts; attempt++ {
		err := s.notifyOnce(job)
		if err == nil {
			return
		}
		if errors.Is(err, pushapi.ErrNoValidTopics) {
			s.lg.Debug("push notify: no valid topics", zap.String("spaceId", job.spaceId))
			return
		}
		if errors.Is(err, space.ErrPushNotConfigured) {
			s.syncFailure(err, "notify")
			return
		}
		if s.ctx.Err() != nil {
			return
		}
		s.lg.Warn("push notify failed",
			zap.String("spaceId", job.spaceId), zap.Int("attempt", attempt), zap.Error(err))
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(notifyRetryDelay):
		}
	}
}

func (s *Service) notifyOnce(job notifyJob) error {
	ctx, cancel := context.WithTimeout(s.ctx, syncRoundBudget)
	defer cancel()
	if job.silent {
		return s.push().NotifySilent(ctx, job.spaceId, job.groupId)
	}
	return s.push().Notify(ctx, job.spaceId, job.topics, job.payload, job.groupId)
}
