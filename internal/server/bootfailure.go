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

// StoreCorrupt reports a boot failure caused by sdk.db failing its
// integrity check on a dirty open. A check cut short by a cancelled boot
// is not a damaged store.
func StoreCorrupt(err error) bool {
	return errors.Is(err, anystore.ErrQuickCheckFailed) &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
