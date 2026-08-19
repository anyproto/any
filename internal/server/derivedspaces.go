package server

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
	// Seed feeds space.DeriveRequest.Seed. Deterministic per account:
	// same account + same seed = same spaceId on every device.
	Seed string
}

var derivedSpaceDefs = []derivedSpaceDef{
	// bao — the account's agent space (bobrik and friends).
	{Name: "bao", Seed: "any/space/bao/v1"},
}

// derivedSpaceByName resolves a registry entry by its wire slug.
func derivedSpaceByName(name string) (derivedSpaceDef, bool) {
	for _, def := range derivedSpaceDefs {
		if def.Name == name {
			return def, true
		}
	}
	return derivedSpaceDef{}, false
}
