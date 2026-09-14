package indexer

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-store/v2/query"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

// Structured search joins the existing full-text cursor to live SDK rows.
// Nothing is persisted twice: changing a type or author never requires a text
// reindex. The cursor is exhausted and closed before opening any SDK dataset.
// Filtering and object deduplication precede the one final offset/limit.
func (ix *Indexer) searchFiltered(ctx context.Context, spaceId string, req api.SearchRequest) (api.SearchResponse, error) {
	sp, err := ix.spaces().Get(ctx, spaceId)
	if err != nil {
		return api.SearchResponse{}, err
	}
	f := req.Filter
	var related map[string]bool
	if f.RelatedTo != nil {
		related, err = ix.store.relatedObjects(ctx, spaceId, *f.RelatedTo)
		if err != nil {
			return api.SearchResponse{}, err
		}
	}
	owners, err := searchOwners(ctx, sp, f.TypeIds, related)
	if err != nil {
		return api.SearchResponse{}, err
	}
	datasets := recordSearchDatasets(sp.Datasets(), req.Scopes)
	wantObjects := len(f.Kinds) == 0 || slices.Contains(f.Kinds, "object")
	wantRecords := len(f.Kinds) == 0 || slices.Contains(f.Kinds, "record")
	browse := strings.TrimSpace(req.Query) == ""
	var hits []api.SearchHit
	if browse {
		if wantObjects && (len(req.Scopes) == 0 || slices.Contains(req.Scopes, index.ScopeBasic) || slices.Contains(req.Scopes, index.ScopeProps)) {
			for _, owner := range owners {
				if slices.Contains(owner.TypeIds, "__type__") {
					continue
				}
				if f.Creator == "" || owner.Creator == f.Creator {
					hits = append(hits, owner)
				}
			}
		}
		if wantRecords {
			for _, ds := range datasets {
				for id, owner := range owners {
					if !hasSearchType(owner.TypeIds, ds.owners) {
						continue
					}
					rows, err := searchRecords(ctx, sp, id, ds, nil, f.Creator)
					if err != nil {
						return api.SearchResponse{}, err
					}
					for _, row := range rows {
						row.TypeIds = owner.TypeIds
						hits = append(hits, row)
					}
				}
			}
		}
	} else {
		text := req.Query
		if ix.opts.StopWords && !strings.Contains(text, `"`) {
			text = stripStopWords(text)
		}
		// Zero means every lexical match, not the public limit (max 100).
		// A matching author/type after the old cutoff must still be found.
		indexed, err := ix.store.SearchFTSQuery(ctx, spaceId, FTSQuery{
			Query: text, DefaultAnd: ix.opts.FTSDefaultAnd, Require: req.Require, Exclude: req.Exclude,
		}, req.Scopes, 0)
		if err != nil {
			return api.SearchResponse{}, err
		}
		bestObjects := map[string]Hit{}
		recordHits := map[[2]string][]Hit{}
		for _, hit := range indexed {
			owner, ok := owners[hit.ObjectId]
			if !ok {
				continue // stale index entry after deletion, or object filter
			}
			if hit.Scope == index.ScopeBasic || hit.Scope == index.ScopeProps {
				if !wantObjects || slices.Contains(owner.TypeIds, "__type__") || (f.Creator != "" && owner.Creator != f.Creator) {
					continue
				}
				if prev, ok := bestObjects[hit.ObjectId]; !ok || hitLess(hit, prev) {
					bestObjects[hit.ObjectId] = hit
				}
			} else if ds, ok := datasets[hit.Dataset]; wantRecords && ok && hasSearchType(owner.TypeIds, ds.owners) {
				key := [2]string{hit.ObjectId, hit.Dataset}
				recordHits[key] = append(recordHits[key], hit)
			}
		}
		for id, hit := range bestObjects {
			row := owners[id]
			row.Scope, row.Dataset, row.RecordId, row.Chunk = hit.Scope, hit.Dataset, hit.RecordId, hit.Chunk
			row.Data, row.Score = hit.Data, hit.Score
			hits = append(hits, row)
		}
		// One live dataset query per (object, dataset), never per hit. The
		// record's creator is read here, not borrowed from the owner.
		for key, indexed := range recordHits {
			groups := groupHits(indexed, 0, req.Passages)
			ids := make([]string, len(groups))
			for i, g := range groups {
				ids[i] = g.Hit.RecordId
			}
			rows, err := searchRecords(ctx, sp, key[0], datasets[key[1]], ids, f.Creator)
			if err != nil {
				return api.SearchResponse{}, err
			}
			for _, group := range groups {
				hit := group.Hit
				row, ok := rows[hit.RecordId]
				if !ok {
					continue
				}
				row.TypeIds = owners[key[0]].TypeIds
				row.Chunk, row.Data, row.Score = hit.Chunk, hit.Data, hit.Score
				for _, p := range group.Passages {
					row.Passages = append(row.Passages, api.SearchPassage{Chunk: p.Chunk, Data: p.Data, Score: p.Score})
				}
				hits = append(hits, row)
			}
		}
	}
	sort := req.Sort
	if sort == "" && browse {
		sort = "modified"
	}
	slices.SortFunc(hits, func(a, b api.SearchHit) int {
		var order int
		switch sort {
		case "modified":
			order = compareSearchDate(a.ModifiedAt, b.ModifiedAt)
		case "created":
			order = compareSearchDate(a.CreatedAt, b.CreatedAt)
		default:
			order = cmp.Compare(b.Score, a.Score)
		}
		return cmp.Or(order, strings.Compare(a.ObjectId, b.ObjectId), strings.Compare(a.Kind, b.Kind), strings.Compare(a.Dataset, b.Dataset), strings.Compare(a.RecordId, b.RecordId))
	})
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	start := min(req.Offset, len(hits))
	end := start + min(limit, len(hits)-start)
	hasNext := end < len(hits)
	out := api.SearchResponse{Hits: append([]api.SearchHit{}, hits[start:end]...), Mode: api.SearchModeFTS, VectorStatus: api.VectorStatusSkipped, HasNext: &hasNext}
	if ix.opts.Embedder == nil {
		out.VectorStatus = api.VectorStatusDisabled
	}
	maxData := req.MaxData
	if maxData == 0 {
		maxData = api.DefaultSearchMaxData
	}
	terms := foldTerms(snippetTerms(req.Query, req.Require))
	for i := range out.Hits {
		h := &out.Hits[i]
		h.Data, h.DataOffset, h.DataTotal = snippet(h.Data, terms, maxData)
		for j := range h.Passages {
			p := &h.Passages[j]
			p.Data, p.DataOffset, p.DataTotal = snippet(p.Data, terms, maxData)
		}
	}
	return out, nil
}

func searchOwners(ctx context.Context, sp space.Space, types []string, related map[string]bool) (map[string]api.SearchHit, error) {
	q := sp.QueryObjects()
	if len(types) > 0 {
		q = q.Filter(searchIn([]string{"any", "types"}, types))
	}
	it, err := q.Iter(ctx)
	if err != nil {
		return nil, err
	}
	defer it.Close()
	out := map[string]api.SearchHit{}
	for it.Next() {
		v, err := it.Doc()
		if err != nil {
			return nil, err
		}
		id := string(v.GetStringBytes("id"))
		if related != nil && !related[id] {
			continue
		}
		types := []string{}
		for _, t := range v.GetArray("any", "types") {
			types = append(types, string(t.GetStringBytes()))
		}
		out[id] = api.SearchHit{Kind: "object", Scope: index.ScopeBasic, ObjectId: id, Dataset: "objects", RecordId: id,
			Title: string(v.GetStringBytes("any", "name")), TypeIds: types,
			Creator: string(v.GetStringBytes("author")), CreatedAt: searchDate(v.Get("createdAt")), ModifiedAt: searchDate(v.Get("modifiedAt"))}
	}
	return out, it.Err()
}

type searchDataset struct {
	name, scope, title, creator, created, modified string
	text                                           []string
	owners                                         []string
}

// Search annotations are the same discovery contract used by SchemaChunker.
// Chat's compiled schema predates x-stamp, so its known stamps are explicit.
func recordSearchDatasets(catalog []space.DatasetSchema, scopes []string) map[string]searchDataset {
	out := map[string]searchDataset{}
	for _, ds := range catalog {
		if len(ds.Owners) == 0 || ds.Module == "editor" {
			continue
		}
		var schema struct {
			Search struct {
				Scope string
				Title string
				Text  json.RawMessage
			} `json:"x-search"`
			Properties map[string]struct {
				Stamp string `json:"x-stamp"`
			} `json:"properties"`
		}
		if json.Unmarshal(ds.JSONSchema, &schema) != nil {
			continue
		}
		s := searchDataset{name: ds.Name, scope: schema.Search.Scope, title: schema.Search.Title, owners: ds.Owners}
		if ds.Module == "chat" {
			s.scope, s.creator, s.created, s.modified = index.ScopeChat, "creator", "createdAt", "modifiedAt"
			s.text = []string{"text"}
		} else {
			if s.scope == "" {
				s.scope = index.ScopeBasic
			}
			var field string
			if json.Unmarshal(schema.Search.Text, &field) == nil && field != "" {
				s.text = []string{field}
			} else {
				_ = json.Unmarshal(schema.Search.Text, &s.text)
			}
			for key, prop := range schema.Properties {
				switch prop.Stamp {
				case "creator":
					s.creator = key
				case "createTime":
					s.created = key
				case "modifyTime":
					s.modified = key
				}
			}
		}
		if s.scope == index.ScopeBasic || s.scope == index.ScopeProps || s.scope == "agent" || s.scope == "history" || !index.ValidScope(s.scope) {
			continue
		}
		if len(scopes) > 0 && !slices.Contains(scopes, s.scope) {
			continue
		}
		if len(s.text) > 0 || s.title != "" {
			out[ds.Name] = s
		}
	}
	return out
}

func searchRecords(ctx context.Context, sp space.Space, objectId string, ds searchDataset, ids []string, creator string) (map[string]api.SearchHit, error) {
	out := map[string]api.SearchHit{}
	if creator != "" && ds.creator == "" {
		return out, nil
	}
	q := sp.Query(objectId, ds.name)
	var filter query.And
	if ids != nil {
		filter = append(filter, searchIn([]string{"id"}, ids))
	}
	if creator != "" {
		filter = append(filter, query.Key{Path: []string{ds.creator}, Filter: query.NewComp(query.CompOpEq, creator)})
	}
	if len(filter) > 0 {
		q = q.Filter(filter)
	}
	it, err := q.Iter(ctx)
	if err != nil {
		if errors.Is(err, space.ErrObjectNotFound) || errors.Is(err, space.ErrObjectDeleted) {
			return out, nil // owner disappeared after the metadata snapshot
		}
		return nil, err
	}
	defer it.Close()
	for it.Next() {
		v, err := it.Doc()
		if err != nil {
			return nil, err
		}
		id := string(v.GetStringBytes("id"))
		parts := []string{}
		for _, field := range ds.text {
			if text := string(v.GetStringBytes(field)); text != "" {
				parts = append(parts, text)
			}
		}
		title := string(v.GetStringBytes(ds.title))
		data := strings.Join(parts, "\n")
		if title == "" {
			title, _, _ = strings.Cut(data, "\n")
		} else {
			data = title + "\n" + data
		}
		out[id] = api.SearchHit{Kind: "record", Scope: ds.scope, ObjectId: objectId, Dataset: ds.name, RecordId: id,
			Title: title, Data: data, Creator: string(v.GetStringBytes(ds.creator)), CreatedAt: searchDate(v.Get(ds.created)), ModifiedAt: searchDate(v.Get(ds.modified))}
	}
	return out, it.Err()
}

func hasSearchType(types, owners []string) bool {
	for _, id := range owners {
		if slices.Contains(types, id) {
			return true
		}
	}
	return false
}

func searchIn(path, values []string) query.Filter {
	a := &anyenc.Arena{}
	vals := make([]*anyenc.Value, len(values))
	for i, value := range values {
		vals[i] = a.NewString(value)
	}
	return query.Key{Path: path, Filter: query.NewInValue(vals...)}
}

func searchDate(v *anyenc.Value) *api.SearchDate {
	if v == nil {
		return nil
	}
	t, err := v.DateTime()
	if err != nil {
		return nil
	}
	return &api.SearchDate{Date: t}
}

func compareSearchDate(a, b *api.SearchDate) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return 1
	}
	if b == nil {
		return -1
	}
	return b.Date.Compare(a.Date)
}
