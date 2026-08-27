package indexer

import "errors"

// ErrIndexRebuildRequired marks a store-open (or dimension-check) failure
// whose ONLY remedy is deleting the index db and letting it rebuild from
// the CRDT — a schema version this build can't read, or a vector
// dimension that contradicts the one the db was built with. Nothing in
// the db is salvageable and no migration exists: the index is a derived
// cache, so the fix is always "remove it, it re-indexes on the next
// change" (docs/13-index.md).
//
// It exists so hosts can key recovery UI off errors.Is instead of the
// error text. The mobile bridges are the consumers: the iOS c-archive
// (mobile/ios) maps it to start code 4 (indexRebuildRequired), which is
// the whole reason a boot failure now carries a code AND a message. The
// Android gomobile bind (mobile/android) surfaces only the string today
// and its host substring-matches it; that stays working (see the
// wrapping note on the three call sites in store.go) until Android
// adopts the same code.
//
// Wrapped as a PREFIX, so the human-readable remedy — which db to
// remove — stays in the message the host shows the user.
var ErrIndexRebuildRequired = errors.New("indexer: index rebuild required")
