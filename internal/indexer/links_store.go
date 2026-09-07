package indexer

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"

	"github.com/anyproto/any/anyuri"
	"github.com/anyproto/any/internal/index"
)

// The link sink — one collection per space next to the text docs,
// written in the same page transaction behind the same cursor
// (docs/13-index.md § Links).
//
// Doc shape: {id, objectId, dataset, recordId, kind, target{…},
// targetKey, targetObject?, applySeq}. `id` is
// `objectId:dataset:recordId:<hash(kind, targetKey)>` — the text-doc
// grammar plus one segment — so every removal is a primary-key range:
// `objectId:` (object deleted), `objectId:dataset:` (type detached,
// definition retired, a reconciled collection rewritten) and
// `objectId:dataset:recordId:` (record re-extracted or deleted). The
// hash merges duplicates from one place: the same (kind, target) in a
// record is one edge. `targetKey` is the canonical target as written
// (a block link stays a block link); `targetObject` is the object it
// belongs to, absent for identities and files.

const (
	linksCollSuffix = "_links"
	// linksSchemaVersion is the link sink's layout version, pinned per
	// space on the cursors row (`links`). A space whose stamp is behind
	// is backfilled from its records — every object re-extracted once,
	// text docs untouched — before its worker advances.
	linksSchemaVersion = 1
)

// LinkDoc is one stored edge.
type LinkDoc struct {
	ObjectId string
	Dataset  string
	RecordId string
	Kind     string
	Target   anyuri.URI
	ApplySeq uint64
}

// LinkOps is one page's link changes: record-level replaces (every
// listed record prefix is cleared, then Ups re-inserts what the
// records now carry), collection-level rewrites (Prefixes: a
// reconciled collection's edges are replaced whole) and the upserts.
// Structural evictions (object / dataset prefixes) ride the page's
// shared prefix deletes, which the sink applies to its collection too.
type LinkOps struct {
	Ups      []index.LinkEntry
	Seqs     []uint64 // ApplySeq per Ups entry
	Dels     []string // ':'-terminated objectId:dataset:recordId: prefixes
	Prefixes []string // ':'-terminated objectId:dataset: prefixes (rewrites)
}

func (o *LinkOps) empty() bool {
	return o == nil || (len(o.Ups) == 0 && len(o.Dels) == 0 && len(o.Prefixes) == 0)
}

// linkDocId builds the edge's primary key.
func linkDocId(e index.LinkEntry) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(e.Kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(e.Target.String()))
	return linkRecordPrefix(e.ObjectId, e.Dataset, e.RecordId) + strconv.FormatUint(h.Sum64(), 36)
}

// linkRecordPrefix is the ':'-terminated prefix of one record's edges.
func linkRecordPrefix(objectId, dataset, recordId string) string {
	return objectId + ":" + dataset + ":" + recordId + ":"
}

// linksColl opens (and indexes) the space's link collection.
func (s *Store) linksColl(ctx context.Context, spaceId string) (anystore.Collection, error) {
	name := spaceId + linksCollSuffix
	s.mu.Lock()
	if coll, ok := s.colls[name]; ok {
		s.mu.Unlock()
		return coll, nil
	}
	s.mu.Unlock()
	coll, err := s.db.Collection(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := coll.EnsureIndex(ctx,
		anystore.IndexInfo{Name: "idx_target_object", Fields: []string{"targetObject"}, Sparse: true},
		anystore.IndexInfo{Name: "idx_target_key", Fields: []string{"targetKey"}},
	); err != nil {
		return nil, fmt.Errorf("indexer: ensure link indexes for %s: %w", spaceId, err)
	}
	s.mu.Lock()
	s.colls[name] = coll
	s.mu.Unlock()
	return coll, nil
}

// applyLinks lands one page's link ops inside txCtx: the shared
// structural prefixes and the page's own record / collection prefixes
// are cleared first, then the upserts. Returns the distinct target
// keys (object keys when the target belongs to an object) whose edge
// set changed — removed ones read before the delete — for the
// liveness signal.
func (s *Store) applyLinks(txCtx context.Context, coll anystore.Collection, prefixDels []string, ops *LinkOps) ([]string, error) {
	touched := map[string]struct{}{}
	noteDoc := func(v *anyenc.Value) {
		if k := v.GetStringBytes("targetObject"); len(k) > 0 {
			touched[string(k)] = struct{}{}
			return
		}
		if k := v.GetStringBytes("targetKey"); len(k) > 0 {
			touched[string(k)] = struct{}{}
		}
	}
	clear := func(prefix string) error {
		idRange := query.And{
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, prefix)},
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, prefixUpper(prefix))},
		}
		iter, err := coll.Find(idRange).Iter(txCtx)
		if err != nil {
			return err
		}
		for iter.Next() {
			doc, err := iter.Doc()
			if err != nil {
				_ = iter.Close()
				return err
			}
			noteDoc(doc.Value())
		}
		err = iter.Err()
		_ = iter.Close()
		if err != nil {
			return err
		}
		_, err = coll.Find(idRange).Delete(txCtx)
		return err
	}
	for _, p := range prefixDels {
		if err := clear(p); err != nil {
			return nil, err
		}
	}
	if ops != nil {
		for _, p := range ops.Prefixes {
			if err := clear(p); err != nil {
				return nil, err
			}
		}
		for _, p := range ops.Dels {
			if err := clear(p); err != nil {
				return nil, err
			}
		}
		gone := newRemovals(nil, prefixDels)
		arena := &anyenc.Arena{}
		for i, e := range ops.Ups {
			id := linkDocId(e)
			if gone.covers(id) {
				continue // the object was evicted in this page
			}
			doc := arena.NewObject()
			doc.Set("id", arena.NewString(id))
			doc.Set("objectId", arena.NewString(e.ObjectId))
			doc.Set("dataset", arena.NewString(e.Dataset))
			doc.Set("recordId", arena.NewString(e.RecordId))
			doc.Set("kind", arena.NewString(e.Kind))
			doc.Set("target", targetValue(arena, e.Target))
			key := e.Target.String()
			doc.Set("targetKey", arena.NewString(key))
			if ok, _ := e.Target.ObjectKey(); ok != "" {
				doc.Set("targetObject", arena.NewString(ok))
				touched[ok] = struct{}{}
			} else {
				touched[key] = struct{}{}
			}
			if i < len(ops.Seqs) {
				doc.Set("applySeq", arena.NewNumberInt(int(ops.Seqs[i])))
			}
			if err := coll.UpsertOne(txCtx, doc); err != nil {
				return nil, err
			}
		}
	}
	if len(touched) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(touched))
	for k := range touched {
		out = append(out, k)
	}
	return out, nil
}

// targetValue stores the canonical target structurally so readers
// need no re-parse.
func targetValue(a *anyenc.Arena, u anyuri.URI) *anyenc.Value {
	obj := a.NewObject()
	obj.Set("kind", a.NewString(string(u.Kind)))
	obj.Set("spaceId", a.NewString(u.SpaceId))
	set := func(k, v string) {
		if v != "" {
			obj.Set(k, a.NewString(v))
		}
	}
	set("objectId", u.ObjectId)
	set("dataset", u.Dataset)
	set("recordId", u.RecordId)
	set("propId", u.PropId)
	set("identity", u.Identity)
	set("fileId", u.FileId)
	return obj
}

func linkDocFrom(v *anyenc.Value) LinkDoc {
	t := v.Get("target")
	d := LinkDoc{
		ObjectId: string(v.GetStringBytes("objectId")),
		Dataset:  string(v.GetStringBytes("dataset")),
		RecordId: string(v.GetStringBytes("recordId")),
		Kind:     string(v.GetStringBytes("kind")),
		ApplySeq: uint64(v.GetInt("applySeq")),
	}
	if t != nil {
		d.Target = anyuri.URI{
			Kind:     anyuri.Kind(t.GetStringBytes("kind")),
			SpaceId:  string(t.GetStringBytes("spaceId")),
			ObjectId: string(t.GetStringBytes("objectId")),
			Dataset:  string(t.GetStringBytes("dataset")),
			RecordId: string(t.GetStringBytes("recordId")),
			PropId:   string(t.GetStringBytes("propId")),
			Identity: string(t.GetStringBytes("identity")),
			FileId:   string(t.GetStringBytes("fileId")),
		}
	}
	return d
}

// Static filter pieces for the link collection.
var (
	targetObjectPath = []string{"targetObject"}
	targetKeyPath    = []string{"targetKey"}
)

// Backlinks returns the edges pointing at key: every target that
// belongs to the object when byObject (the object itself, its records,
// its property values), or exactly the target when not. kinds narrows
// (empty = all); limit bounds the read (0 = default cap). Ordered by
// id — source object, dataset, record.
func (s *Store) Backlinks(ctx context.Context, spaceId, key string, byObject bool, kinds []string, limit int) ([]LinkDoc, error) {
	coll, err := s.linksColl(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	path := targetKeyPath
	if byObject {
		path = targetObjectPath
	}
	var filter query.Filter = query.Key{Path: path, Filter: query.NewComp(query.CompOpEq, key)}
	if len(kinds) > 0 {
		filter = query.And{filter, kindFilter(kinds)}
	}
	return s.collectLinks(ctx, coll, filter, limit)
}

// Links returns the edges whose source is under prefix — an object
// (`objectId:`), one of its datasets (`objectId:dataset:`) or one
// record (`objectId:dataset:recordId:`) — narrowed by kinds.
func (s *Store) Links(ctx context.Context, spaceId, prefix string, kinds []string, limit int) ([]LinkDoc, error) {
	coll, err := s.linksColl(ctx, spaceId)
	if err != nil {
		return nil, err
	}
	var filter query.Filter = query.And{
		query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, prefix)},
		query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, prefixUpper(prefix))},
	}
	if len(kinds) > 0 {
		filter = query.And{filter, kindFilter(kinds)}
	}
	return s.collectLinks(ctx, coll, filter, limit)
}

// kindFilter narrows to the given kinds ($in).
func kindFilter(kinds []string) query.Filter {
	a := &anyenc.Arena{}
	vals := make([]*anyenc.Value, len(kinds))
	for i, k := range kinds {
		vals[i] = a.NewString(k)
	}
	return query.Key{Path: []string{"kind"}, Filter: query.NewInValue(vals...)}
}

// maxLinksRead bounds one link read.
const maxLinksRead = 1000

func (s *Store) collectLinks(ctx context.Context, coll anystore.Collection, filter query.Filter, limit int) ([]LinkDoc, error) {
	if limit <= 0 || limit > maxLinksRead {
		limit = maxLinksRead
	}
	iter, err := coll.Find(filter).Sort("id").Limit(uint(limit)).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var out []LinkDoc
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		out = append(out, linkDocFrom(doc.Value()))
	}
	return out, iter.Err()
}

// LinkSpaces lists the spaces holding a link collection — the
// account-wide read iterates them.
func (s *Store) LinkSpaces(ctx context.Context) ([]string, error) {
	names, err := s.db.GetCollectionNames(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		if strings.HasSuffix(n, linksCollSuffix) {
			out = append(out, strings.TrimSuffix(n, linksCollSuffix))
		}
	}
	return out, nil
}

// dropLinks drops the space's link collection (DropSpace).
func (s *Store) dropLinks(ctx context.Context, spaceId string) error {
	name := spaceId + linksCollSuffix
	s.mu.Lock()
	delete(s.colls, name)
	s.mu.Unlock()
	coll, err := s.db.OpenCollection(ctx, name)
	if err != nil {
		if errors.Is(err, anystore.ErrCollectionNotFound) {
			return nil
		}
		return err
	}
	return coll.Drop(ctx)
}

// LinksVersion reads the link-sink layout version stamped on the
// space's cursor row (0 = never stamped).
func (s *Store) LinksVersion(ctx context.Context, spaceId string) (int, error) {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return 0, err
	}
	doc, err := coll.FindId(ctx, spaceId)
	if errors.Is(err, anystore.ErrDocNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return doc.Value().GetInt("links"), nil
}

// LinksBackfillNeeded reports whether the space's edges predate the
// current link-sink layout: an indexed space (cursor > 0) whose stamp
// is behind. A never-indexed space needs none — its first advance
// extracts everything.
func (s *Store) LinksBackfillNeeded(ctx context.Context, spaceId string) (bool, error) {
	cursor, _, err := s.Cursor(ctx, spaceId)
	if err != nil {
		return false, err
	}
	if cursor == 0 {
		return false, nil
	}
	v, err := s.LinksVersion(ctx, spaceId)
	if err != nil {
		return false, err
	}
	return v != linksSchemaVersion, nil
}

// ReplaceLinks rewrites every edge of the space from a full extraction
// (the backfill): the collection is dropped and the ops applied in one
// transaction, then the layout version is stamped. Text docs are not
// touched.
func (s *Store) ReplaceLinks(ctx context.Context, spaceId string, ops *LinkOps) error {
	if err := s.dropLinks(ctx, spaceId); err != nil {
		return err
	}
	coll, err := s.linksColl(ctx, spaceId)
	if err != nil {
		return err
	}
	tx, err := s.db.WriteTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck — no-op after Commit
	if _, err := s.applyLinks(tx.Context(), coll, nil, ops); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.stampLinksVersion(ctx, spaceId)
}

// stampLinksVersion marks the space's edges as written on the current
// layout (merged into the cursor row).
func (s *Store) stampLinksVersion(ctx context.Context, spaceId string) error {
	return s.stampLinksVersionAs(ctx, spaceId, linksSchemaVersion)
}

func (s *Store) stampLinksVersionAs(ctx context.Context, spaceId string, version int) error {
	coll, err := s.db.Collection(ctx, cursorsCollection)
	if err != nil {
		return err
	}
	mod := query.ModifyFunc(func(a *anyenc.Arena, v *anyenc.Value) (*anyenc.Value, bool, error) {
		v.Set("links", a.NewNumberInt(version))
		return v, true, nil
	})
	_, err = coll.UpsertId(ctx, spaceId, mod)
	return err
}
