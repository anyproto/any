package server

import (
	"context"
	"errors"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/go-sqlite"
)

// StorageFull reports a boot failure caused by a full disk: the OS error
// from the account dir, the device key or an any-store v2 file, or
// SQLITE_FULL from an any-sync (any-store v1) store.
func StorageFull(err error) bool {
	return diskFull(err) || sqlite.ErrCode(err).ToPrimary() == sqlite.ResultFull
}

// StoreCorrupt reports a boot failure caused by a damaged sdk.db: a
// broken header or WAL, or a page that fails its checksum. Not
// anystore.ErrCorrupt: that also matches a quick check cut short by a
// read error, which is not damage. A cancelled or timed-out boot never
// answers with a deletion.
func StoreCorrupt(err error) bool {
	return (errors.Is(err, anystore.ErrPageIntegrity) ||
		errors.Is(err, anystore.ErrHeaderCorrupt) ||
		errors.Is(err, anystore.ErrWALCorrupt)) &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
