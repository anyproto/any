package indexer

// Reproduces any-kotlin's AnyRuntimeImpl.isRebuildableIndexFailure()
// against the messages this package actually produces.
//
// Android has no start code yet, so its whole index-recovery path hangs
// off substring-matching this text. That makes the wording a wire
// contract with another repo that nothing else in either build would
// catch: rewording a message here silently stops a user's index from
// ever being rebuilt, with no compile error and no failing test. It is
// also why the sentinel is wrapped as a PREFIX rather than replacing the
// text — the substrings stay in every tail.
//
// Delete this file once Android keys off ErrIndexRebuildRequired /
// start code 4.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// isRebuildableIndexFailure is a transliteration of any-kotlin
// app/src/main/java/com/anytypeio/anytype/app/runtime/AnyRuntimeImpl.kt
// (the `message.lowercase()` + three `contains` checks). The cause-chain
// walk is dropped: gomobile flattens the Go error chain into one string,
// which is the case that matters here.
func isRebuildableIndexFailure(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "indexer:") &&
		strings.Contains(m, "remove ") &&
		strings.Contains(m, "to rebuild")
}

func TestAndroidMatcherStillRecognizesRebuildFailures(t *testing.T) {
	ctx := context.Background()

	for name, msg := range map[string]string{
		"schema mismatch": openStoreErrText(t, ctx, indexSchemaVersion+1),
		"dim mismatch":    dimMismatchErrText(t, ctx),
		"EnsureDim":       ensureDimErrText(t, ctx),
	} {
		t.Run(name, func(t *testing.T) {
			if !isRebuildableIndexFailure(msg) {
				t.Fatalf("any-kotlin would no longer offer an index rebuild for this failure:\n  %s", msg)
			}
		})
	}
}

// TestAndroidMatcherRejectsUnrelatedFailures keeps the test honest: a
// predicate that returned true for everything would pass the case above
// while telling us nothing. A generic boot failure must NOT trigger a
// wipe of the user's index.
func TestAndroidMatcherRejectsUnrelatedFailures(t *testing.T) {
	for _, msg := range []string{
		"embedded: bad data directory",
		"embedded: server boot failed: listen tcp 127.0.0.1:7001: bind: address already in use",
		"indexer: open index db: disk I/O error",
	} {
		if isRebuildableIndexFailure(msg) {
			t.Fatalf("any-kotlin would wipe the index for an unrelated failure: %q", msg)
		}
	}
}

func openStoreErrText(t *testing.T, ctx context.Context, schema int) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "index.db")
	seedSchema(t, ctx, path, schema)

	st, err := OpenStore(ctx, path, 0, false, 0)
	if err == nil {
		st.Close()
		t.Fatal("OpenStore on a foreign schema succeeded")
	}
	return err.Error()
}

func dimMismatchErrText(t *testing.T, ctx context.Context) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "index.db")
	st, err := OpenStore(ctx, path, 768, true, 0)
	if err != nil {
		t.Fatalf("OpenStore (first, dim 768): %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st, err = OpenStore(ctx, path, 1024, true, 0)
	if err == nil {
		st.Close()
		t.Fatal("OpenStore with a contradicting dim succeeded")
	}
	return err.Error()
}

func ensureDimErrText(t *testing.T, ctx context.Context) string {
	t.Helper()

	st, err := OpenStoreInMemory(ctx, 768, true, 0)
	if err != nil {
		t.Fatalf("OpenStoreInMemory: %v", err)
	}
	defer st.Close()

	err = st.EnsureDim(ctx, 1024)
	if err == nil {
		t.Fatal("EnsureDim with a contradicting dim succeeded")
	}
	return err.Error()
}
