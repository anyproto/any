package embedded

import (
	"errors"

	"github.com/anyproto/any/internal/indexer"
	"github.com/anyproto/any/internal/server"
)

// Start codes: the outcome of Start as both mobile shims return it. 0
// means the server is up; any other code means it is not, and the error
// message says why. Hosts mirror these values: never renumber, only add.
const (
	// CodeOK: the server is up and bound.
	CodeOK int32 = 0
	// CodeAlreadyRunning: a server is already started in this process
	// (ErrAlreadyRunning). Stop it before starting again.
	CodeAlreadyRunning int32 = 1
	// CodeBadDataDir: the data dir is empty or cannot be created
	// (ErrBadDataDir).
	CodeBadDataDir int32 = 2
	// CodeBootFailed: any other boot failure (*BootError).
	CodeBootFailed int32 = 3
	// CodeIndexRebuildRequired: the boot could not rebuild a search index
	// it refused (indexer.ErrIndexRebuildRequired). The host deletes the
	// account's index dir and retries.
	CodeIndexRebuildRequired int32 = 4
	// CodeBadOptions: the mode, the control token or the nodeconf is
	// invalid (ErrBadOptions). A host bug, never retryable.
	CodeBadOptions int32 = 5
	// CodeStorageFull: the disk is full (server.StorageFull). The host
	// frees space and retries.
	CodeStorageFull int32 = 6
	// CodeStoreCorrupt: the account's sdk.db has a broken header or WAL, or
	// a page that fails its checksum (server.StoreCorrupt). It is never
	// repaired in place.
	CodeStoreCorrupt int32 = 7
)

// Code maps a Start error to its start code; nil is CodeOK. A full disk
// is checked first: freeing space is the fix even when it broke the data
// dir or an index rebuild.
func Code(err error) int32 {
	switch {
	case err == nil:
		return CodeOK
	case server.StorageFull(err):
		return CodeStorageFull
	case errors.Is(err, ErrAlreadyRunning):
		return CodeAlreadyRunning
	case errors.Is(err, ErrBadDataDir):
		return CodeBadDataDir
	case errors.Is(err, ErrBadOptions):
		return CodeBadOptions
	case errors.Is(err, indexer.ErrIndexRebuildRequired):
		return CodeIndexRebuildRequired
	case server.StoreCorrupt(err):
		return CodeStoreCorrupt
	default:
		return CodeBootFailed
	}
}
