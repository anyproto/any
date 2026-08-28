// Package localstore is the naming, existence and tag fence for the
// server's device-local any-store collections (task-local-store.md).
//
// Local collections are plain any-store collections — no CRDT, no
// handler, never synced — that live in the SDK's own sdk.db so that a
// pipeline can one day join or roll synced data into them. They share
// the file under one contract: every local collection carries the "l_"
// tag, the SDK never emits a tagged name, and nothing in this package
// can address an untagged one. ParseRef / ParseStorageName are that
// contract's single chokepoint; the tag is applied by Ref.StorageName
// and nowhere else.
//
// The store owns no lifecycle: the SDK opens and closes the DB, the
// store borrows the handle.
package localstore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	anystore "github.com/anyproto/any-store/v2"
)

// Scope is where a local collection is bound.
type Scope string

const (
	// ScopeAccount lives as long as the account dir. Storage name "l_a_<name>".
	ScopeAccount Scope = "account"
	// ScopeSpace is bound to a space by id. Storage name
	// "l_s_<spaceId>_<name>". Nothing drops it when the space goes — a
	// space-scoped collection outlives its space (task-local-store.md).
	ScopeSpace Scope = "space"
)

const (
	tag        = "l"
	segAccount = "a"
	segSpace   = "s"
	sep        = "_"
	tagPrefix  = tag + sep
)

var (
	// ErrBadName reports a scope / spaceId / name that fails validation.
	ErrBadName = errors.New("localstore: bad collection reference")
	// ErrNotFound reports a reference whose collection has not been ensured.
	ErrNotFound = errors.New("localstore: collection not found")
	// ErrNotLocal reports a storage name that does not carry the local tag —
	// an SDK-owned collection this package refuses to address.
	ErrNotLocal = errors.New("localstore: not a local collection")
)

// nameRe bounds a collection name. "_" is legal inside a name: the
// fixed segments before it make the storage-name split unambiguous.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// spaceIdRe is not a CID check. It only pins the charset that keeps the
// storage-name split exact: a space id (cid "." network id, both base
// alphabets) never contains "_", so the first "_" after the scope
// segment always ends the space id.
var spaceIdRe = regexp.MustCompile(`^[A-Za-z0-9.]{1,256}$`)

// Ref addresses one local collection.
type Ref struct {
	Scope   Scope
	SpaceId string // ScopeSpace only
	Name    string
}

// ParseRef validates the wire triple and returns the Ref. It is the one
// chokepoint every path goes through — wire bodies, internal Drop, and
// $out / $merge / $lookup targets (via SinkTarget).
func ParseRef(scope Scope, spaceId, name string) (Ref, error) {
	if !nameRe.MatchString(name) {
		return Ref{}, fmt.Errorf("%w: name %q", ErrBadName, name)
	}
	switch scope {
	case ScopeAccount:
		if spaceId != "" {
			return Ref{}, fmt.Errorf("%w: spaceId is not allowed for account scope", ErrBadName)
		}
		return Ref{Scope: ScopeAccount, Name: name}, nil
	case ScopeSpace:
		if !spaceIdRe.MatchString(spaceId) {
			return Ref{}, fmt.Errorf("%w: spaceId %q", ErrBadName, spaceId)
		}
		return Ref{Scope: ScopeSpace, SpaceId: spaceId, Name: name}, nil
	default:
		return Ref{}, fmt.Errorf("%w: scope %q", ErrBadName, scope)
	}
}

// ParseStorageName is the inverse of StorageName: the fixed-segment
// split of an any-store collection name. ok is false for every untagged
// name (SDK collections included) and for a tagged name that does not
// re-validate through ParseRef.
func ParseStorageName(s string) (ref Ref, ok bool) {
	rest, hasTag := strings.CutPrefix(s, tagPrefix)
	if !hasTag {
		return Ref{}, false
	}
	seg, rest, found := strings.Cut(rest, sep)
	if !found {
		return Ref{}, false
	}
	var err error
	switch seg {
	case segAccount:
		ref, err = ParseRef(ScopeAccount, "", rest)
	case segSpace:
		spaceId, name, found := strings.Cut(rest, sep)
		if !found {
			return Ref{}, false
		}
		ref, err = ParseRef(ScopeSpace, spaceId, name)
	default:
		return Ref{}, false
	}
	if err != nil {
		return Ref{}, false
	}
	return ref, true
}

// StorageName is the tagged any-store name — the only place the tag is
// applied. Exposed for callers that must hand a name to any-store
// themselves (sink-target rewriting).
func (r Ref) StorageName() string {
	switch r.Scope {
	case ScopeSpace:
		return tagPrefix + segSpace + sep + r.SpaceId + sep + r.Name
	default:
		return tagPrefix + segAccount + sep + r.Name
	}
}

// listPrefix is the storage-name prefix shared by every collection List
// would return for (scope, spaceId); scope "" covers all local collections.
func listPrefix(scope Scope, spaceId string) string {
	switch scope {
	case ScopeAccount:
		return tagPrefix + segAccount + sep
	case ScopeSpace:
		if spaceId == "" {
			return tagPrefix + segSpace + sep
		}
		return tagPrefix + segSpace + sep + spaceId + sep
	default:
		return tagPrefix
	}
}

// Info describes an existing local collection.
type Info struct {
	Ref
	Count   int
	Indexes []anystore.IndexInfo
}

// Store fences a borrowed any-store DB to the local tag.
type Store struct {
	db anystore.DB
}

// New wraps the SDK's DB handle. The store never opens or closes it.
func New(db anystore.DB) *Store {
	return &Store{db: db}
}

// Ensure creates the collection if absent and ensures the given indexes
// on it. created reports whether this call made it.
func (s *Store) Ensure(ctx context.Context, ref Ref, indexes []anystore.IndexInfo) (created bool, err error) {
	name := ref.StorageName()
	coll, err := s.db.OpenCollection(ctx, name)
	switch {
	case err == nil:
	case errors.Is(err, anystore.ErrCollectionNotFound):
		coll, err = s.db.CreateCollection(ctx, name)
		if errors.Is(err, anystore.ErrCollectionExists) {
			// Lost a race with a concurrent Ensure: adopt.
			coll, err = s.db.OpenCollection(ctx, name)
		} else if err == nil {
			created = true
		}
		if err != nil {
			return false, err
		}
	default:
		return false, err
	}
	if len(indexes) > 0 {
		if err := coll.EnsureIndex(ctx, indexes...); err != nil {
			return created, err
		}
	}
	return created, nil
}

// Drop removes the collection and its data. Missing is ErrNotFound.
func (s *Store) Drop(ctx context.Context, ref Ref) error {
	coll, err := s.Collection(ctx, ref)
	if err != nil {
		return err
	}
	return coll.Drop(ctx)
}

// Collection returns the handle for an ensured collection, ErrNotFound
// otherwise. The DB caches open handles; callers do not close them.
func (s *Store) Collection(ctx context.Context, ref Ref) (anystore.Collection, error) {
	coll, err := s.db.OpenCollection(ctx, ref.StorageName())
	if errors.Is(err, anystore.ErrCollectionNotFound) {
		return nil, ErrNotFound
	}
	return coll, err
}

// List enumerates local collections. scope "" lists every local
// collection; ScopeSpace with an empty spaceId lists every space-scoped
// one. Untagged names are never returned. Order is the DB's (sorted by
// storage name).
func (s *Store) List(ctx context.Context, scope Scope, spaceId string) ([]Info, error) {
	if scope != "" && scope != ScopeAccount && scope != ScopeSpace {
		return nil, fmt.Errorf("%w: scope %q", ErrBadName, scope)
	}
	if scope == ScopeSpace && spaceId != "" && !spaceIdRe.MatchString(spaceId) {
		return nil, fmt.Errorf("%w: spaceId %q", ErrBadName, spaceId)
	}
	names, err := s.db.GetCollectionNames(ctx)
	if err != nil {
		return nil, err
	}
	prefix := listPrefix(scope, spaceId)
	var out []Info
	for _, name := range names {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		ref, ok := ParseStorageName(name)
		if !ok {
			continue
		}
		coll, err := s.db.OpenCollection(ctx, name)
		if err != nil {
			if errors.Is(err, anystore.ErrCollectionNotFound) {
				continue // dropped between enumerate and open
			}
			return nil, err
		}
		count, err := coll.Count(ctx)
		if err != nil {
			return nil, err
		}
		info := Info{Ref: ref, Count: count}
		for _, ix := range coll.GetIndexes() {
			info.Indexes = append(info.Indexes, ix.Info())
		}
		out = append(out, info)
	}
	return out, nil
}

// SinkTarget validates a collection name a client put INSIDE a pipeline
// ($out / $merge target, $lookup from) — the one place a raw storage
// name arrives from the wire. It must be a tagged name that round-trips
// through ParseRef; anything else is ErrNotLocal.
func SinkTarget(storageName string) (Ref, error) {
	ref, ok := ParseStorageName(storageName)
	if !ok {
		return Ref{}, fmt.Errorf("%w: %q", ErrNotLocal, storageName)
	}
	return ref, nil
}
