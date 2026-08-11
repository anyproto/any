// The mailbox object hosts a synced address's email_messages +
// email_sync_state datasets. It is derived, not created: every device
// (and every re-bootstrapped sync rig) computes the same object id
// from the address, so there is no discovery query and no create race
// — Objects().Derive is idempotent (creates on first call, resolves
// on every later one). Same mechanic as the agent brain and the
// per-space general chat.
package email

import (
	"context"
	"fmt"
	"strings"

	"github.com/anyproto/any-sync-sdk/space"
)

// MailboxSeedPrefix prefixes the derivation seed. The full seed is
// MailboxSeedPrefix + NormalizeAddress(address); bumping the version
// segment would derive NEW mailbox objects for every address, so it is
// effectively frozen.
const MailboxSeedPrefix = "any/email-mailbox/v1/"

// NormalizeAddress canonicalizes an address for seeding and for the
// derived participants array: trimmed, lowercased. No plus-tag or
// dot stripping — those are provider policies, not address identity.
func NormalizeAddress(addr string) string {
	return strings.ToLower(strings.TrimSpace(addr))
}

// DeriveMailboxObjectId resolves (creating on first use) the mailbox
// object for the given address. The email type is attached on first
// materialization, so ingest writes are admitted without a separate
// ensureType step; any.name is left to the caller (the rig or UI may
// PATCH it to the display address).
func DeriveMailboxObjectId(ctx context.Context, sp space.Space, address string) (string, error) {
	addr := NormalizeAddress(address)
	if addr == "" || len(addr) > MaxAddrBytes {
		return "", fmt.Errorf("email: derive mailbox: invalid address")
	}
	objectId, err := sp.Objects().Derive(ctx, space.DeriveObjectOpts{
		Seed:  []byte(MailboxSeedPrefix + addr),
		Types: []string{TypeId},
	})
	if err != nil {
		return "", fmt.Errorf("email: derive mailbox object: %w", err)
	}
	return objectId, nil
}
