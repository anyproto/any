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
// targetKey, targetObject?, applySeq}. `id` is the text-doc base
// `objectId:dataset:recordId` + U+001F + `<hash(kind, targetKey)>` —
// the same control-byte separator the text path uses for chunks, so a
// record id containing `:` can never be confused with a sibling's
// prefix: every structural removal is a primary-key range (`objectId:`
// for a deleted object, `objectId:dataset:` for a detached type or a
// retired definition), a record's edges are the range
// `[base+U+001F, base+U+0020)`, and the hash merges duplicates from
// one place: the same (kind, target) in a record is one edge. Content changes are applied as an exact
// diff: the worker reads the object's stored edge ids once, compares
// them with what the records now carry, and lands only the ids that
// appeared or vanished (worker.go diffLinks). `targetKey` is the
// canonical target as written (a block link stays a block link);
// `targetObject` is the object it belongs to, absent for identities
// and files.

const (
	linksCollSuffix = "_links"
	// linksSchemaVersion is the link sink's layout version, pinned per
	// space on the cursors row (`links`). An indexed space whose stamp
	// is behind is backfilled from its records — every object
	// re-extracted once, text docs untouched — by its worker.
	linksSchemaVersion = 1
)

// LinkDoc is one stored edge.
type LinkDoc struct {
	ObjectId string
	Dataset  string
	RecordId string
	TypeId   string // property-value sources only
	Field    string // runtime-record sources with several link fields
	Kind     string
	Target   anyuri.URI
	ApplySeq uint64
}

// LinkOps is one page's link changes, already diffed against the
// store: Dels are exact doc ids to remove, Ups the edges to write,
// Touched the liveness keys either side named. Structural evictions
// (object / dataset prefixes) ride the page's shared prefix deletes,
// which the sink applies to its collection too.
type LinkOps struct {
	Ups     []index.LinkEntry
	Seqs    []uint64 // ApplySeq per Ups entry
	Dels    []string
	Touched []string
}

func (o *LinkOps) empty() bool {
	return o == nil || (len(o.Ups) == 0 && len(o.Dels) == 0)
}

// linkDocId builds the edge's primary key.
func linkDocId(e index.LinkEntry) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(e.Kind))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(e.Field))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(e.Target.String()))
	return linkRecordPrefix(e.ObjectId, e.Dataset, e.RecordId) + strconv.FormatUint(h.Sum64(), 36)
}

// linkRecordPrefix is the prefix of one record's edges: the text-doc
// base plus the control-byte separator (chunk.go). Exact — a record id
// never carries a control byte (indexableId), so no other record's
// edges start with it.
func linkRecordPrefix(objectId, dataset, recordId string) string {
	return docId(objectId, dataset, recordId) + chunkSep
}

// linkRecordUpper is the exclusive upper bound of one record's edges.
func linkRecordUpper(prefix string) string {
	return prefix[:len(prefix)-len(chunkSep)] + recordRangeEnd
}

// linkTargetKey is the liveness key of a target: the object it
// belongs to, or the target itself for an identity or a file.
func linkTargetKey(u anyuri.URI) string {
	if ok, has := u.ObjectKey(); has {
		return ok
	}
	return u.String()
}

// linksColl opens (and indexes) the space's link collection for
// writing — creating it on first use.
//
// The lock is held across the open: dropLinks evicts the cache entry
// before dropping, and an open racing that window must not put a
// handle back that is about to be closed.
func (s *Store) linksColl(ctx context.Context, spaceId string) (anystore.Collection, error) {
	name := spaceId + linksCollSuffix
	s.mu.Lock()
	defer s.mu.Unlock()
	if coll, ok := s.colls[name]; ok {
		return coll, nil
	}
	coll, err := s.db.Collection(ctx, name)
	if err != nil {
		return nil, err
	}
	// Compound with the id so a capped read is a prefix scan in id
	// order, not a materialised sort.
	if err := coll.EnsureIndex(ctx,
		anystore.IndexInfo{Name: "idx_target_object_id", Fields: []string{"targetObject", "id"}, Sparse: true},
		anystore.IndexInfo{Name: "idx_target_key_id", Fields: []string{"targetKey", "id"}},
	); err != nil {
		return nil, fmt.Errorf("indexer: ensure link indexes for %s: %w", spaceId, err)
	}
	s.colls[name] = coll
	return coll, nil
}

// linksCollRead opens the space's link collection for a read without
// creating it: nil when the space has never had edges written. Not
// cached — reads are rare next to the worker's writes, and a cached
// read handle could outlive a backfill's drop.
func (s *Store) linksCollRead(ctx context.Context, spaceId string) (anystore.Collection, error) {
	name := spaceId + linksCollSuffix
	s.mu.Lock()
	coll, ok := s.colls[name]
	s.mu.Unlock()
	if ok {
		return coll, nil
	}
	coll, err := s.db.OpenCollection(ctx, name)
	if errors.Is(err, anystore.ErrCollectionNotFound) {
		return nil, nil
	}
	return coll, err
}

// StoredLink is what the worker's diff needs of a stored edge.
type StoredLink struct {
	Key string // liveness key (linkTargetKey)
}

// LinkIds returns the stored edge ids under prefix (an object,
// `objectId:`), each with its liveness key — the worker diffs a
// changed object's new edge set against it.
func (s *Store) LinkIds(ctx context.Context, spaceId, prefix string) (map[string]StoredLink, error) {
	coll, err := s.linksCollRead(ctx, spaceId)
	if err != nil || coll == nil {
		return nil, err
	}
	idRange := query.And{
		query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, prefix)},
		query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, prefixUpper(prefix))},
	}
	iter, err := coll.Find(idRange).Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	out := map[string]StoredLink{}
	for iter.Next() {
		doc, err := iter.Doc()
		if err != nil {
			return nil, err
		}
		v := doc.Value()
		key := string(v.GetStringBytes("targetObject"))
		if key == "" {
			key = string(v.GetStringBytes("targetKey"))
		}
		out[string(v.GetStringBytes("id"))] = StoredLink{Key: key}
	}
	return out, iter.Err()
}

// applyLinks lands one page's link ops inside txCtx: the shared
// structural prefixes are cleared first (their docs read for the
// liveness keys they held), then the exact deletes, then the upserts.
// Returns the distinct liveness keys whose edge set changed.
func (s *Store) applyLinks(txCtx context.Context, coll anystore.Collection, prefixDels []string, ops *LinkOps) ([]string, error) {
	touched := map[string]struct{}{}
	for _, p := range prefixDels {
		idRange := query.And{
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, p)},
			query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, prefixUpper(p))},
		}
		iter, err := coll.Find(idRange).Iter(txCtx)
		if err != nil {
			return nil, err
		}
		for iter.Next() {
			doc, err := iter.Doc()
			if err != nil {
				_ = iter.Close()
				return nil, err
			}
			v := doc.Value()
			if k := v.GetStringBytes("targetObject"); len(k) > 0 {
				touched[string(k)] = struct{}{}
			} else if k := v.GetStringBytes("targetKey"); len(k) > 0 {
				touched[string(k)] = struct{}{}
			}
		}
		err = iter.Err()
		_ = iter.Close()
		if err != nil {
			return nil, err
		}
		if _, err := coll.Find(idRange).Delete(txCtx); err != nil {
			return nil, err
		}
	}
	if ops != nil {
		for _, id := range ops.Dels {
			if err := coll.DeleteId(txCtx, id); err != nil && !errors.Is(err, anystore.ErrDocNotFound) {
				return nil, err
			}
		}
		gone := newRemovals(nil, prefixDels)
		arena := &anyenc.Arena{}
		for i, e := range ops.Ups {
			id := linkDocId(e)
			if gone.covers(id) {
				continue // the object was evicted in this page: removal wins
			}
			doc := arena.NewObject()
			doc.Set("id", arena.NewString(id))
			doc.Set("objectId", arena.NewString(e.ObjectId))
			doc.Set("dataset", arena.NewString(e.Dataset))
			doc.Set("recordId", arena.NewString(e.RecordId))
			if e.TypeId != "" {
				doc.Set("typeId", arena.NewString(e.TypeId))
			}
			if e.Field != "" {
				doc.Set("field", arena.NewString(e.Field))
			}
			doc.Set("kind", arena.NewString(e.Kind))
			doc.Set("target", targetValue(arena, e.Target))
			doc.Set("targetKey", arena.NewString(e.Target.String()))
			if objKey, has := e.Target.ObjectKey(); has {
				doc.Set("targetObject", arena.NewString(objKey))
			}
			if i < len(ops.Seqs) {
				doc.Set("applySeq", arena.NewNumberInt(int(ops.Seqs[i])))
			}
			if err := coll.UpsertOne(txCtx, doc); err != nil {
				return nil, err
			}
		}
		for _, k := range ops.Touched {
			touched[k] = struct{}{}
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
		TypeId:   string(v.GetStringBytes("typeId")),
		Field:    string(v.GetStringBytes("field")),
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

// Backlinks returns the edges pointing at key: every target that
// belongs to the object when byObject (the object itself, its records,
// its property values), or exactly the target when not. kinds narrows
// (empty = all); limit bounds the read (0 = the cap). Ordered by id —
// source object, dataset, record. more reports that the read was cut
// at limit.
func (s *Store) Backlinks(ctx context.Context, spaceId, key string, byObject bool, kinds []string, limit int) (docs []LinkDoc, more bool, err error) {
	coll, err := s.linksCollRead(ctx, spaceId)
	if err != nil || coll == nil {
		return nil, false, err
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
//
// A record prefix ends in the control-byte separator (linkRecordPrefix)
// and is bounded by linkRecordUpper; the others end in ':' and by
// prefixUpper.
func (s *Store) Links(ctx context.Context, spaceId, prefix string, kinds []string, limit int) (docs []LinkDoc, more bool, err error) {
	coll, err := s.linksCollRead(ctx, spaceId)
	if err != nil || coll == nil {
		return nil, false, err
	}
	upper := prefixUpper(prefix)
	if strings.HasSuffix(prefix, chunkSep) {
		upper = linkRecordUpper(prefix)
	}
	var filter query.Filter = query.And{
		query.Key{Path: idPath, Filter: query.NewComp(query.CompOpGte, prefix)},
		query.Key{Path: idPath, Filter: query.NewComp(query.CompOpLt, upper)},
	}
	if len(kinds) > 0 {
		filter = query.And{filter, kindFilter(kinds)}
	}
	return s.collectLinks(ctx, coll, filter, limit)
}

func (s *Store) collectLinks(ctx context.Context, coll anystore.Collection, filter query.Filter, limit int) ([]LinkDoc, bool, error) {
	if limit <= 0 || limit > maxLinksRead {
		limit = maxLinksRead
	}
	iter, err := coll.Find(filter).Sort("id").Limit(uint(limit + 1)).Iter(ctx)
	if err != nil {
		return nil, false, err
	}
	defer iter.Close()
	var out []LinkDoc
	for iter.Next() {
		if len(out) == limit {
			return out, true, nil
		}
		doc, err := iter.Doc()
		if err != nil {
			return nil, false, err
		}
		out = append(out, linkDocFrom(doc.Value()))
	}
	return out, false, iter.Err()
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

// dropLinks drops the space's link collection (DropSpace, backfill).
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
// extracts everything and stamps.
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

// ApplyLinks lands link ops on their own — the backfill's per-page
// write, text docs untouched. One transaction per call.
func (s *Store) ApplyLinks(ctx context.Context, spaceId string, ops *LinkOps) error {
	if ops.empty() {
		return nil
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
	return tx.Commit()
}

// ResetLinks drops the space's edges ahead of a backfill.
func (s *Store) ResetLinks(ctx context.Context, spaceId string) error {
	return s.dropLinks(ctx, spaceId)
}

// StampLinksVersion marks the space's edges as written on the current
// layout (merged into the cursor row): after a backfill, and after
// the first page an advance lands.
func (s *Store) StampLinksVersion(ctx context.Context, spaceId string) error {
	return s.stampLinksVersionAs(ctx, spaceId, linksSchemaVersion)
}

// UnstampLinks forgets the space's layout stamp — the state of a db
// indexed before the link sink existed. A test seam for the backfill.
func (s *Store) UnstampLinks(ctx context.Context, spaceId string) error {
	return s.stampLinksVersionAs(ctx, spaceId, 0)
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
