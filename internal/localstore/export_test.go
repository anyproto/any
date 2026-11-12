package localstore

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/stretchr/testify/require"
)

func docsOf(t *testing.T, coll anystore.Collection) map[string]string {
	t.Helper()
	iter, err := coll.Find(nil).Iter(context.Background())
	require.NoError(t, err)
	defer iter.Close()
	out := map[string]string{}
	for iter.Next() {
		doc, err := iter.Doc()
		require.NoError(t, err)
		out[doc.Value().GetString("id")] = doc.Value().String()
	}
	require.NoError(t, iter.Err())
	return out
}

// The round trip: two collections (account + space scope, one with
// indexes, one empty) export into one file whose manifest describes
// both, and import into a fresh store recreates names, indexes and
// documents byte-for-byte. A second import is a no-op (upsert).
func TestExportImport_RoundTrip(t *testing.T) {
	ctx := context.Background()
	src := New(openDB(t))
	notes, _ := ParseRef(ScopeAccount, "", "notes")
	traces, _ := ParseRef(ScopeSpace, spaceA, "trace_records")
	empty, _ := ParseRef(ScopeSpace, spaceA, "trace_blobs")
	_, err := src.Ensure(ctx, notes, nil)
	require.NoError(t, err)
	_, err = src.Ensure(ctx, traces, []anystore.IndexInfo{
		{Fields: []string{"runId", "seq"}, Unique: true},
		{Fields: []string{"error.type"}, Sparse: true},
	})
	require.NoError(t, err)
	_, err = src.Ensure(ctx, empty, nil)
	require.NoError(t, err)

	notesColl, _ := src.Collection(ctx, notes)
	tracesColl, _ := src.Collection(ctx, traces)
	a := &anyenc.Arena{}
	for i := 0; i < 3; i++ {
		d := a.NewObject()
		d.Set("id", a.NewString(string(rune('a'+i))))
		d.Set("n", a.NewNumberInt(i))
		require.NoError(t, notesColl.Insert(ctx, d))
	}
	// more than one import chunk, with nested + binary payloads
	for i := 0; i < importChunk+7; i++ {
		d := a.NewObject()
		d.Set("id", a.NewString("run_1:"+string(rune('0'+i%10))+string(rune('A'+i/10))))
		d.Set("runId", a.NewString("run_1"))
		d.Set("seq", a.NewNumberInt(i))
		in := a.NewObject()
		in.Set("bytes", a.NewBinary([]byte{0, 1, 2, 255}))
		in.Set("text", a.NewString("héllo\x00world"))
		d.Set("input", in)
		require.NoError(t, tracesColl.Insert(ctx, d))
	}

	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, []Ref{notes, traces, empty, notes}, &buf))

	// the file is gzip around an anyenc stream whose first value is
	// the manifest
	gz, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	mv, err := anyenc.NewReader(gz).Read(&anyenc.Parser{})
	require.NoError(t, err)
	m, err := manifestOf(mv)
	require.NoError(t, err)
	require.Len(t, m.Collections, 3, "duplicate ref exported once")
	require.Equal(t, "l_a_notes", m.Collections[0].StorageName)
	require.Equal(t, 3, m.Collections[0].Count)
	require.Equal(t, "l_s_"+spaceA+"_trace_records", m.Collections[1].StorageName)
	require.Equal(t, importChunk+7, m.Collections[1].Count)
	require.Len(t, m.Collections[1].Indexes, 2)
	require.True(t, m.Collections[1].Indexes[0].Unique)
	require.True(t, m.Collections[1].Indexes[1].Sparse)
	require.Equal(t, 0, m.Collections[2].Count)

	dst := New(openDB(t))
	infos, err := dst.Import(ctx, bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	require.Len(t, infos, 3)
	require.Equal(t, traces, infos[1].Ref)
	require.Equal(t, importChunk+7, *infos[1].Count)
	require.Len(t, infos[1].Indexes, 2)

	for _, ref := range []Ref{notes, traces, empty} {
		s, err := src.Collection(ctx, ref)
		require.NoError(t, err)
		d, err := dst.Collection(ctx, ref)
		require.NoError(t, err, ref.StorageName())
		require.Equal(t, docsOf(t, s), docsOf(t, d), ref.StorageName())
	}

	// idempotent: the same file again changes nothing
	infos, err = dst.Import(ctx, bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)
	require.Equal(t, importChunk+7, *infos[1].Count)
	d, _ := dst.Collection(ctx, traces)
	n, _ := d.Count(ctx)
	require.Equal(t, importChunk+7, n)
}

func TestExport_MissingCollection(t *testing.T) {
	s := New(openDB(t))
	ref, _ := ParseRef(ScopeAccount, "", "nope")
	var buf bytes.Buffer
	err := s.Export(context.Background(), []Ref{ref}, &buf)
	require.ErrorIs(t, err, ErrNotFound)
	require.Zero(t, buf.Len(), "nothing written before the resolve")
}

// Every way a file can be wrong is ErrBadExport, and the fence holds:
// a manifest naming an untagged collection never reaches any-store.
func TestImport_BadFiles(t *testing.T) {
	ctx := context.Background()
	s := New(openDB(t))

	gzOf := func(values ...*anyenc.Value) []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		w := anyenc.NewWriter(gz)
		for _, v := range values {
			require.NoError(t, w.Write(v))
		}
		require.NoError(t, w.Flush())
		require.NoError(t, gz.Close())
		return buf.Bytes()
	}
	a := &anyenc.Arena{}
	ref, _ := ParseRef(ScopeAccount, "", "notes")
	manifest := func(count int) *anyenc.Value {
		return manifestValue(a, &Manifest{Format: FormatName, Version: FormatVersion,
			Collections: []ManifestCollection{{Ref: ref, StorageName: ref.StorageName(), Count: count}}})
	}
	doc := func(id string) *anyenc.Value {
		d := a.NewObject()
		d.Set("id", a.NewString(id))
		return d
	}

	_, err := s.Import(ctx, bytes.NewReader([]byte("not gzip")))
	require.ErrorIs(t, err, ErrBadExport)

	_, err = s.Import(ctx, bytes.NewReader(gzOf(a.NewString("manifest?"))))
	require.ErrorIs(t, err, ErrBadExport)

	foreign := manifestValue(a, &Manifest{Format: FormatName, Version: FormatVersion + 1})
	_, err = s.Import(ctx, bytes.NewReader(gzOf(foreign)))
	require.ErrorIs(t, err, ErrBadExport)

	// truncated: the manifest promises two documents, one arrives
	_, err = s.Import(ctx, bytes.NewReader(gzOf(manifest(2), doc("a"))))
	require.ErrorIs(t, err, ErrBadExport)
	var ie *ImportError
	require.ErrorAs(t, err, &ie)
	require.Equal(t, ref, ie.Ref)

	// data past the last section
	_, err = s.Import(ctx, bytes.NewReader(gzOf(manifest(1), doc("a"), doc("b"))))
	require.ErrorIs(t, err, ErrBadExport)

	// a section value that is not a document
	_, err = s.Import(ctx, bytes.NewReader(gzOf(manifest(1), a.NewString("x"))))
	require.ErrorIs(t, err, ErrBadExport)

	// a document without an id
	_, err = s.Import(ctx, bytes.NewReader(gzOf(manifest(1), a.NewObject())))
	require.ErrorIs(t, err, ErrBadExport)

	// the fence: a storage name that is not the derived one, and a
	// scope the store does not know
	tampered := manifestValue(a, &Manifest{Format: FormatName, Version: FormatVersion,
		Collections: []ManifestCollection{{Ref: ref, StorageName: "_meta", Count: 0}}})
	_, err = s.Import(ctx, bytes.NewReader(gzOf(tampered)))
	require.ErrorIs(t, err, ErrBadExport)
	names, _ := s.db.GetCollectionNames(ctx)
	require.NotContains(t, names, "_meta")
	bad := manifestValue(a, &Manifest{Format: FormatName, Version: FormatVersion,
		Collections: []ManifestCollection{{Ref: Ref{Scope: "global", Name: "x"}, StorageName: "l_a_x", Count: 0}}})
	_, err = s.Import(ctx, bytes.NewReader(gzOf(bad)))
	require.ErrorIs(t, err, ErrBadExport)

	// a good file after all that still imports — no state leaked
	infos, err := s.Import(ctx, bytes.NewReader(gzOf(manifest(2), doc("a"), doc("b"))))
	require.NoError(t, err)
	require.Equal(t, 2, *infos[0].Count)

	// an index under an existing name with a different definition is
	// any-store's mismatch, attributed to the collection
	withIx := manifestValue(a, &Manifest{Format: FormatName, Version: FormatVersion,
		Collections: []ManifestCollection{{Ref: ref, StorageName: ref.StorageName(), Count: 0,
			Indexes: []ManifestIndex{{Name: "k", Fields: []string{"k"}}}}}})
	_, err = s.Import(ctx, bytes.NewReader(gzOf(withIx)))
	require.NoError(t, err)
	clash := manifestValue(a, &Manifest{Format: FormatName, Version: FormatVersion,
		Collections: []ManifestCollection{{Ref: ref, StorageName: ref.StorageName(), Count: 0,
			Indexes: []ManifestIndex{{Name: "k", Fields: []string{"k"}, Unique: true}}}}})
	_, err = s.Import(ctx, bytes.NewReader(gzOf(clash)))
	require.True(t, errors.Is(err, anystore.ErrIndexMismatch), "%v", err)
	require.ErrorAs(t, err, &ie)
}

// A stalled upload holds no write transaction: while the body is cut
// off mid-chunk, another write on the same DB commits.
func TestImport_StalledBodyHoldsNoWriter(t *testing.T) {
	ctx := context.Background()
	s := New(openDB(t))
	other, _ := ParseRef(ScopeAccount, "", "other")
	_, err := s.Ensure(ctx, other, nil)
	require.NoError(t, err)
	otherColl, err := s.Collection(ctx, other)
	require.NoError(t, err)

	a := &anyenc.Arena{}
	ref, _ := ParseRef(ScopeAccount, "", "notes")
	doc := func(id string) *anyenc.Value {
		d := a.NewObject()
		d.Set("id", a.NewString(id))
		return d
	}
	// the manifest and one of two documents, then the body stalls
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	w := anyenc.NewWriter(gz)
	require.NoError(t, w.Write(manifestValue(a, &Manifest{Format: FormatName, Version: FormatVersion,
		Collections: []ManifestCollection{{Ref: ref, StorageName: ref.StorageName(), Count: 2}}})))
	require.NoError(t, w.Write(doc("a")))
	require.NoError(t, w.Flush())
	require.NoError(t, gz.Flush())
	stallAt := buf.Len()
	require.NoError(t, w.Write(doc("b")))
	require.NoError(t, w.Flush())
	require.NoError(t, gz.Close())
	body := &stallReader{data: buf.Bytes(), stallAt: stallAt, blocked: make(chan struct{}), resume: make(chan struct{})}
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(body.resume) }) }
	done := make(chan error, 1)
	go func() {
		_, err := s.Import(ctx, body)
		done <- err
	}()
	// on failure: release the body and join the import before the DB closes
	t.Cleanup(func() {
		resume()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	// Import consumed everything sent and waits for the second
	// document; a write transaction open here would block the insert.
	select {
	case <-body.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("import never reached the stall")
	}
	inserted := make(chan error, 1)
	go func() { inserted <- otherColl.Insert(ctx, anyenc.MustParseJson(`{"id":"x"}`)) }()
	t.Cleanup(func() {
		resume() // cleanups run last-first: release the body before joining
		select {
		case <-inserted:
		case <-time.After(5 * time.Second):
		}
	})
	select {
	case err := <-inserted:
		inserted <- nil // the cleanup's join
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("a write blocked behind a stalled import")
	}
	resume()
	require.NoError(t, <-done)
	done <- nil // the cleanup's join
	coll, err := s.Collection(ctx, ref)
	require.NoError(t, err)
	require.Len(t, docsOf(t, coll), 2)
}

// stallReader serves data, blocking once at stallAt until resume closes;
// blocked closes when a Read reaches the stall.
type stallReader struct {
	data            []byte
	stallAt, off    int
	stalled         bool
	blocked, resume chan struct{}
}

func (r *stallReader) Read(p []byte) (int, error) {
	if r.off == r.stallAt && !r.stalled {
		r.stalled = true
		close(r.blocked)
		<-r.resume
	}
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	end := len(r.data)
	if !r.stalled {
		end = r.stallAt
	}
	n := copy(p, r.data[r.off:end])
	r.off += n
	return n, nil
}
