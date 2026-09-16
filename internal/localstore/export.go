package localstore

// Export / import — a collection-level dump of the local store
// The file is a gzip
// stream (anyenc carries no checksum; gzip's CRC catches payload
// corruption) around ONE anyenc value stream (any-store v2.1.1
// `anyenc.Writer` / `anyenc.Reader`): the manifest object first, then
// every document of every listed collection, in manifest order,
// exactly `count` each. No separators, no trailer: the manifest's
// counts delimit the sections, and they are exact because the whole
// export runs inside one read transaction — one snapshot, so a dump is
// consistent across collections.
//
// The fence holds on the way in too: import never trusts the recorded
// storage name — every collection is re-derived from its (scope,
// spaceId, name) triple through ParseRef, so a crafted file cannot
// address an SDK collection.

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
)

const (
	// FormatName / FormatVersion identify the manifest. A reader
	// refuses any other pair — there is no compatibility promise
	// across versions.
	FormatName    = "any-local-export"
	FormatVersion = 1
	// importChunk is the per-transaction write size on import — the
	// same 256 every /v1/local write uses: any-store has one writer
	// per DB and a long transaction stalls every CRDT apply.
	importChunk = 256
)

// ErrBadExport reports a file that is not an export this version
// reads: not gzip, not an anyenc stream, a foreign or newer manifest,
// a section shorter than its count, a non-object document, or bytes
// past the last section.
var ErrBadExport = errors.New("localstore: bad export file")

// Manifest is the stream's first value.
type Manifest struct {
	Format  string
	Version int
	// ExportedAt is unix milliseconds, informational.
	ExportedAt  int64
	Collections []ManifestCollection
}

// ManifestCollection describes one section: the collection's ref, its
// tagged storage name (informational — import re-derives it), the
// exact number of documents that follow, and the range indexes to
// recreate.
type ManifestCollection struct {
	Ref
	StorageName string
	Count       int
	Indexes     []ManifestIndex
}

// ManifestIndex is a range index: the only kind the local store
// exposes, and the only kind import will create.
type ManifestIndex struct {
	Name   string
	Fields []string
	Unique bool
	Sparse bool
}

// ImportError names the collection an import failed on. Sections
// before it are committed (chunked writes, like every /v1/local
// write); the failing collection may be partially written.
type ImportError struct {
	Ref Ref
	Err error
}

func (e *ImportError) Error() string { return e.Ref.StorageName() + ": " + e.Err.Error() }
func (e *ImportError) Unwrap() error { return e.Err }

// Export streams the named collections to w as one gzip'd anyenc
// stream. Everything is resolved before the first byte — a missing
// collection is ErrNotFound (wrapped with its storage name), never a
// truncated stream. Duplicates in refs are exported once.
func (s *Store) Export(ctx context.Context, refs []Ref, w io.Writer) error {
	tx, err := s.db.ReadTx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Commit() }()
	txCtx := tx.Context()

	m := Manifest{Format: FormatName, Version: FormatVersion, ExportedAt: time.Now().UnixMilli()}
	var colls []anystore.Collection
	seen := map[string]bool{}
	for _, ref := range refs {
		name := ref.StorageName()
		if seen[name] {
			continue
		}
		seen[name] = true
		coll, err := s.db.OpenCollection(txCtx, name)
		if errors.Is(err, anystore.ErrCollectionNotFound) {
			return fmt.Errorf("%w: %s", ErrNotFound, name)
		}
		if err != nil {
			return err
		}
		count, err := coll.Count(txCtx)
		if err != nil {
			return err
		}
		mc := ManifestCollection{Ref: ref, StorageName: name, Count: count}
		for _, ix := range coll.GetIndexes() {
			info := ix.Info()
			mc.Indexes = append(mc.Indexes, ManifestIndex{Name: info.Name, Fields: info.Fields, Unique: info.Unique, Sparse: info.Sparse})
		}
		m.Collections = append(m.Collections, mc)
		colls = append(colls, coll)
	}

	gz := gzip.NewWriter(w)
	enc := anyenc.NewWriter(gz)
	if err := enc.Write(manifestValue(&anyenc.Arena{}, &m)); err != nil {
		return err
	}
	for i, coll := range colls {
		n, err := exportSection(txCtx, coll, enc)
		if err != nil {
			return fmt.Errorf("%s: %w", m.Collections[i].StorageName, err)
		}
		if n != m.Collections[i].Count {
			// cannot happen inside one read tx; a reader would misparse
			// every later section, so refuse to hand over the file
			return fmt.Errorf("%s: counted %d documents, iterated %d", m.Collections[i].StorageName, m.Collections[i].Count, n)
		}
	}
	if err := enc.Flush(); err != nil {
		return err
	}
	return gz.Close()
}

func exportSection(ctx context.Context, coll anystore.Collection, enc *anyenc.Writer) (int, error) {
	iter, err := coll.Find(nil).Iter(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			_ = iter.Close()
			return n, err
		}
		if err := enc.Write(doc.Value()); err != nil {
			_ = iter.Close()
			return n, err
		}
		n++
	}
	if err := iter.Err(); err != nil {
		_ = iter.Close()
		return n, err
	}
	return n, iter.Close()
}

// Import loads an export into this store: each collection is ensured
// with the file's indexes (idempotent — an existing collection keeps
// its documents, a different definition under the same index name is
// any-store's ErrIndexMismatch) and its documents are upserted 256
// per transaction, so re-importing the same file is a no-op and a
// newer export of the same collections overlays the older. No space
// pre-flight: the file's spaces need not exist here — that is the
// point (a reporter's traces on a developer's scratch server). The
// returned Infos cover the collections written, in file order; on an
// error they are the ones completed before the *ImportError.
func (s *Store) Import(ctx context.Context, r io.Reader) ([]Info, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("%w: not gzip: %v", ErrBadExport, err)
	}
	dec := anyenc.NewReader(gz)
	p := &anyenc.Parser{}
	mv, err := dec.Read(p)
	if err != nil {
		return nil, fmt.Errorf("%w: manifest: %v", ErrBadExport, err)
	}
	m, err := manifestOf(mv)
	if err != nil {
		return nil, err
	}
	var out []Info
	for _, mc := range m.Collections {
		ref := mc.Ref
		if _, err := s.Ensure(ctx, ref, importIndexes(mc.Indexes)); err != nil {
			return out, &ImportError{Ref: ref, Err: err}
		}
		coll, err := s.Collection(ctx, ref)
		if err != nil {
			return out, &ImportError{Ref: ref, Err: err}
		}
		if err := importSection(ctx, coll, dec, p, mc.Count); err != nil {
			return out, &ImportError{Ref: ref, Err: err}
		}
		count, err := coll.Count(ctx)
		if err != nil {
			return out, &ImportError{Ref: ref, Err: err}
		}
		info := Info{Ref: ref, Count: count}
		for _, ix := range coll.GetIndexes() {
			info.Indexes = append(info.Indexes, ix.Info())
		}
		out = append(out, info)
	}
	// the stream ends with the last section — anything after it is a
	// file this manifest does not describe
	if _, err := dec.Read(p); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("data past the last collection")
		}
		return out, fmt.Errorf("%w: %v", ErrBadExport, err)
	}
	return out, nil
}

func importSection(ctx context.Context, coll anystore.Collection, dec *anyenc.Reader, p *anyenc.Parser, count int) error {
	for done := 0; done < count; {
		n := min(count-done, importChunk)
		tx, err := coll.WriteTx(ctx)
		if err != nil {
			return err
		}
		for i := range n {
			v, err := dec.Read(p)
			if err != nil {
				_ = tx.Rollback()
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return fmt.Errorf("%w: truncated after document %d of %d", ErrBadExport, done+i, count)
				}
				return fmt.Errorf("%w: document %d: %v", ErrBadExport, done+i, err)
			}
			if v.Type() != anyenc.TypeObject {
				_ = tx.Rollback()
				return fmt.Errorf("%w: document %d is not an object", ErrBadExport, done+i)
			}
			if err := coll.UpsertOne(tx.Context(), v); err != nil {
				_ = tx.Rollback()
				if errors.Is(err, anystore.ErrDocWithoutId) {
					return fmt.Errorf("%w: document %d has no id", ErrBadExport, done+i)
				}
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		done += n
	}
	return nil
}

func importIndexes(in []ManifestIndex) []anystore.IndexInfo {
	out := make([]anystore.IndexInfo, 0, len(in))
	for _, ix := range in {
		out = append(out, anystore.IndexInfo{Name: ix.Name, Fields: ix.Fields, Unique: ix.Unique, Sparse: ix.Sparse})
	}
	return out
}

// manifestValue encodes the manifest as the stream's first value.
func manifestValue(a *anyenc.Arena, m *Manifest) *anyenc.Value {
	root := a.NewObject()
	root.Set("format", a.NewString(m.Format))
	root.Set("version", a.NewNumberInt(m.Version))
	root.Set("exportedAt", a.NewNumberFloat64(float64(m.ExportedAt)))
	colls := a.NewArray()
	for i, mc := range m.Collections {
		cv := a.NewObject()
		cv.Set("scope", a.NewString(string(mc.Scope)))
		if mc.SpaceId != "" {
			cv.Set("spaceId", a.NewString(mc.SpaceId))
		}
		cv.Set("name", a.NewString(mc.Name))
		cv.Set("storageName", a.NewString(mc.StorageName))
		cv.Set("count", a.NewNumberInt(mc.Count))
		ixs := a.NewArray()
		for j, ix := range mc.Indexes {
			iv := a.NewObject()
			iv.Set("name", a.NewString(ix.Name))
			fields := a.NewArray()
			for k, f := range ix.Fields {
				fields.SetArrayItem(k, a.NewString(f))
			}
			iv.Set("fields", fields)
			iv.Set("unique", a.NewBool(ix.Unique))
			iv.Set("sparse", a.NewBool(ix.Sparse))
			ixs.SetArrayItem(j, iv)
		}
		cv.Set("indexes", ixs)
		colls.SetArrayItem(i, cv)
	}
	root.Set("collections", colls)
	return root
}

// manifestOf decodes and validates the first value. Every collection
// ref goes through ParseRef — the recorded storageName is checked
// against the derived one and never used to address anything.
func manifestOf(v *anyenc.Value) (*Manifest, error) {
	if v.Type() != anyenc.TypeObject {
		return nil, fmt.Errorf("%w: manifest is not an object", ErrBadExport)
	}
	m := &Manifest{
		Format:     v.GetString("format"),
		Version:    v.GetInt("version"),
		ExportedAt: int64(v.GetFloat64("exportedAt")),
	}
	if m.Format != FormatName || m.Version != FormatVersion {
		return nil, fmt.Errorf("%w: format %q version %d (this server reads %s version %d)",
			ErrBadExport, m.Format, m.Version, FormatName, FormatVersion)
	}
	for i, cv := range v.GetArray("collections") {
		ref, err := ParseRef(Scope(cv.GetString("scope")), cv.GetString("spaceId"), cv.GetString("name"))
		if err != nil {
			return nil, fmt.Errorf("%w: collection %d: %v", ErrBadExport, i, err)
		}
		mc := ManifestCollection{Ref: ref, StorageName: cv.GetString("storageName"), Count: cv.GetInt("count")}
		if mc.StorageName != ref.StorageName() {
			return nil, fmt.Errorf("%w: collection %d: storageName %q does not match %s",
				ErrBadExport, i, mc.StorageName, ref.StorageName())
		}
		if mc.Count < 0 {
			return nil, fmt.Errorf("%w: collection %d: negative count", ErrBadExport, i)
		}
		for _, iv := range cv.GetArray("indexes") {
			ix := ManifestIndex{Name: iv.GetString("name"), Unique: iv.GetBool("unique"), Sparse: iv.GetBool("sparse")}
			for _, f := range iv.GetArray("fields") {
				ix.Fields = append(ix.Fields, f.GetString())
			}
			if len(ix.Fields) == 0 {
				return nil, fmt.Errorf("%w: collection %d: index %q without fields", ErrBadExport, i, ix.Name)
			}
			mc.Indexes = append(mc.Indexes, ix)
		}
		m.Collections = append(m.Collections, mc)
	}
	return m, nil
}
