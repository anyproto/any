package server

import (
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

// StoreCorrupt reports a boot failure caused by a damaged sdk.db: a page
// that fails its checksum, in the quick check or a later read. A quick
// check that failed on a read error is not damage.
func StoreCorrupt(err error) bool {
	return errors.Is(err, anystore.ErrPageIntegrity)
}
