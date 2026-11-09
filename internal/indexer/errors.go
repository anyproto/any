package indexer

import "errors"

// ErrIndexRebuildRequired marks a store-open (or dimension-check) failure
// whose ONLY remedy is deleting the index db and letting it rebuild from
// the CRDT — a schema version this build can't read, a vector dimension
// or chunk target that contradicts the one the db was built with, or
// damage met while opening it (a failed quick check or page checksum).
// Nothing in the db is salvageable and no migration exists: the index is
// a derived cache (docs/13-index.md).
//
// The boot keys on it with errors.Is: the server removes the db and
// opens a fresh one, so the host sees no error; a rebuild that fails
// keeps it in the chain (start code 4 on the mobile shims). Mid-run,
// EnsureDim returns it when an embedder answers with another dimension,
// and the embed round fails. Wrapped as a PREFIX: the sentinel text
// leads, the cause follows.
var ErrIndexRebuildRequired = errors.New("indexer: index rebuild required")

// ErrIndexNotReady marks a read of a space whose full-text index is
// being rewritten in place (Store.upgradeFTS, the first open after a
// layout change) or has not been created yet: the read must not wait
// on the store's write lock — a rewrite holds it for seconds per large
// space — so it answers at once and the caller retries. The process
// view shows the rewrite as index.fts_upgrade.<spaceId>.
var ErrIndexNotReady = errors.New("indexer: the space's search index is not ready")

// errNoIndex marks a read of a space that was never indexed: there is
// no collection, so the legs answer empty.
var errNoIndex = errors.New("indexer: no index for the space")
