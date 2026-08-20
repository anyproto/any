package server

import (
	"context"
	"fmt"

	anysyncsdk "github.com/anyproto/any-sync-sdk"
	"github.com/anyproto/any-sync-sdk/space"
)

// The embedded derived-space registry: the closed vocabulary of
// well-known per-account spaces `any` derives deterministically from
// the account keys. Raw space derivation is never exposed over HTTP —
// an arbitrary seed would mint a permanent space (derived spaces
// cannot be deleted) and invite silent seed collisions between
// consumers — so only the names listed here can be resolved
// (GET /v1/spaces/derived) and materialized
// (POST /v1/spaces/derived/:name).
//
// Adding an entry: pick a short slug and a versioned seed following
// the `any/space/<name>/v1` convention (same scheme as the derived
// in-space objects — chat.GeneralChatSeed etc.). Seeds are forever:
// never change or reuse one — bump the version suffix for a
// successor space instead. Removing an entry stops advertising the
// name; it deletes nothing (nothing can).

// derivedSpaceDef is one registry entry.
type derivedSpaceDef struct {
	// Name is the wire slug (`/v1/spaces/derived/:name`).
	Name string
	// DisplayName seeds the space's metadata name on first
	// materialization (best-effort; empty = leave nameless).
	DisplayName string
	// Seed feeds space.DeriveRequest.Seed. Deterministic per account:
	// same account + same seed = same spaceId on every device.
	Seed string
}

var derivedSpaceDefs = []derivedSpaceDef{
	// bao — the account's agent space (bobrik and friends).
	{Name: "bao", DisplayName: "bao", Seed: "any/space/bao/v1"},
}

// resolvedDerivedSpace is a registry entry resolved against the booted
// account. The (account, seed) → spaceId mapping is immutable for the
// engine's lifetime, so it is computed once at boot: handlers — the
// DELETE pre-check included — work from an infallible lookup instead
// of re-running the derive crypto per request.
type resolvedDerivedSpace struct {
	derivedSpaceDef
	SpaceId string
}

// resolveDerivedSpaces computes the registry's spaceIds for the booted
// account (pure computation over the account keys — nothing is created
// or loaded).
func resolveDerivedSpaces(ctx context.Context, sdk *anysyncsdk.SDK) ([]resolvedDerivedSpace, error) {
	out := make([]resolvedDerivedSpace, 0, len(derivedSpaceDefs))
	for _, def := range derivedSpaceDefs {
		id, err := sdk.Spaces().DeriveId(ctx, space.DeriveRequest{Seed: []byte(def.Seed)})
		if err != nil {
			return nil, fmt.Errorf("resolve derived space %q: %w", def.Name, err)
		}
		out = append(out, resolvedDerivedSpace{derivedSpaceDef: def, SpaceId: id})
	}
	return out, nil
}

// derivedSpaceByName resolves a registry entry by its wire slug.
func (d *deps) derivedSpaceByName(name string) (resolvedDerivedSpace, bool) {
	for _, r := range d.derived {
		if r.Name == name {
			return r, true
		}
	}
	return resolvedDerivedSpace{}, false
}

// isDerivedSpaceId reports whether spaceId belongs to the registry —
// the DELETE pre-check for derived spaces with no tech-space row yet
// (the SDK's flag-based refusal can't see those; a delete would write
// a sticky tombstone wedging the well-known id). Infallible: the ids
// were resolved at engine boot.
func (d *deps) isDerivedSpaceId(spaceId string) bool {
	for _, r := range d.derived {
		if r.SpaceId == spaceId {
			return true
		}
	}
	return false
}
