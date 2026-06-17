//go:build !vector || gomobile || mobile

package indexer

// capVector is false without the `vector` build tag, and is
// force-false on every gomobile (Android) or mobile (iOS) build
// regardless of tags — vector / embedding is always off on mobile (see
// caps_vector_on.go and docs/13-index.md § build tags).
const capVector = false
