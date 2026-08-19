// Package bao owns the bao space's install — the `bao/v1` record in
// that space's bundles registry (any-sync-sdk docs/bundles.md).
//
// The bao space itself is derived from the account keys (the
// derived-space registry), so its id is the same on every device and
// needs no registry. What the registry buys is the setup INSIDE it:
// once setup writes data it needs a real root object, two devices
// setting up while offline mint two, and only the registry gives them
// a deterministic winner plus a handle on the loser.
//
// The root — bao's "tsar" object — is non-derived (derived trees
// cannot be deleted, so a losing install would be unresolvable).
// Setup objects hang off it as derived children (bundles.Child), which
// makes the winner's id enough to re-derive the whole install on a
// restored device and makes cleanup one cascade delete.
package bao

import (
	"context"
	"fmt"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/bundles"
	"github.com/anyproto/any/internal/page"
)

const (
	// BundleId is the registry record id. Permanent and versioned — a
	// successor setup takes a new id rather than reusing this one.
	BundleId = "bao/v1"
	// BundleName is the display name stamped as `any.name` on the root.
	BundleName = "bao"
)

// Install describes the bao bundle.
//
// Merge is nil: the tsar root carries no children yet — bao's memory,
// config, secrets and chat still live on their own space-derived
// objects — so a losing root holds nothing and deleting it loses
// nothing. Children that carry content bring their own Merge with them.
func Install() bundles.Install {
	return bundles.Install{
		Id:        BundleId,
		Name:      BundleName,
		RootTypes: []string{page.TypeId},
	}
}

// Ensure installs or adopts bao's setup in the space and returns the
// converged registry row. Fully local — no network wait.
func Ensure(ctx context.Context, sp space.Space) (space.Bundle, error) {
	b, err := bundles.Ensure(ctx, sp, Install())
	if err != nil {
		return space.Bundle{}, fmt.Errorf("bao: %w", err)
	}
	return b, nil
}
