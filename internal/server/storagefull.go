package server

import "github.com/anyproto/go-sqlite"

// storageFull reports a boot failure caused by a full disk: the OS error
// from the account dir, the device key or an any-store v2 file, or
// SQLITE_FULL from an any-sync (any-store v1) store.
func storageFull(err error) bool {
	return diskFull(err) || sqlite.ErrCode(err).ToPrimary() == sqlite.ResultFull
}
