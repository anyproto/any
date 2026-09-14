package indexer

import (
	"context"

	"github.com/anyproto/any-store/v2/query"
)

// HostFilter is a request's object filter: a condition over the hit's
// host object row. Those rows live in the SDK's store, not the index,
// so the caller supplies the two reads and hostSet decides how to use
// them (docs/13-index.md § Filtering by object).
type HostFilter interface {
	// Resolve lists the matching object ids. With max > 0 it stops after
	// max ids and reports more when the set continues past them.
	Resolve(ctx context.Context, max int) (ids []string, more bool, err error)
	// Match reports which of ids satisfy the filter, in one read.
	Match(ctx context.Context, ids []string) (map[string]bool, error)
}

// Filter budgets. Variables so tests can shrink the corpus that
// exercises each path; the values are the contract (docs/13-index.md
// § Filtering by object).
var (
	// filterIdsMax is the id-set size up to which a filter is handed to
	// the legs as a residual `objectId $in`: the store's planner then
	// probes the objectId index per candidate instead of walking the
	// posting lists when that is cheaper. Measured to pay up to a few
	// hundred objects; past that the driver plan wins anyway and
	// resolving the ids only costs.
	filterIdsMax = 256
	// filterBatch is how many rows a post-filtered leg pulls before their
	// objects are looked up in one read.
	filterBatch = 64
	// filterScanRows is how many rows a post-filtered leg reads through
	// lookups before the whole id set is materialized: a page still short
	// at this depth means the filter is narrow relative to the corpus,
	// and one resolve then beats any number of further lookups.
	filterScanRows = 5000
	// filterScanRowsMax bounds a leg's read under a filter; a page still
	// short past it is reported truncated.
	filterScanRowsMax = 100000
	// filterMaterializeMax bounds the set a rescue materializes; a
	// larger one stays lazy, so a page short on a broad filter never
	// decodes the whole objects collection.
	filterMaterializeMax = 50000
	// filterResidualMax is the largest exact set a leg takes as a
	// residual `objectId $in`: any-store derives index bounds from an
	// $in of fewer than 10 000 members and none past it. The vector leg
	// resolves a lazy set up to here before it queries — one ANN round
	// with the residual beats re-running the ANN per widening round.
	filterResidualMax = 9999
)

// hostSet is one request's view of the filter. A set the probe found
// complete is carried as ids and rides the legs as a residual;
// otherwise membership is resolved lazily, in batches, and cached per
// object — until materialize replaces the cache with the complete set,
// which the legs then take as a residual too while it fits
// filterResidualMax.
type hostSet struct {
	filter HostFilter
	// ids is the complete set once exact; list keeps it in resolve
	// order and res is the residual built from it once — nil past
	// filterResidualMax.
	ids   map[string]struct{}
	list  []string
	res   query.Filter
	exact bool
	// triedMax is the largest bound a resolve came back short of; a
	// materialize within it is known to fail and is skipped.
	triedMax int
	// small is set when the probe found the complete set within
	// filterIdsMax: few enough docs that probing them one by one is
	// always cheaper than the ANN's beam — and the only way the beam
	// reaches them (hint).
	small bool
	// known caches lazy verdicts while the set is not exact.
	known map[string]bool
}

// newHostSet probes the filter once: a set of at most filterIdsMax ids
// is resolved whole, anything larger starts lazy.
func newHostSet(ctx context.Context, filter HostFilter) (*hostSet, error) {
	if filter == nil {
		return nil, nil
	}
	ids, more, err := filter.Resolve(ctx, filterIdsMax)
	if err != nil {
		return nil, err
	}
	s := &hostSet{filter: filter, known: map[string]bool{}}
	if more {
		s.triedMax = filterIdsMax
	} else {
		s.setExact(ids)
		s.small = true
	}
	return s, nil
}

func (s *hostSet) setExact(ids []string) {
	s.list = ids
	s.ids = make(map[string]struct{}, len(ids))
	for _, id := range ids {
		s.ids[id] = struct{}{}
	}
	s.exact = true
	s.known = nil
	if len(ids) <= filterResidualMax {
		s.res = objectIdIn(ids)
	}
}

// residual returns the `objectId $in` clause for an exact set that
// fits filterResidualMax, nil when the legs must post-filter.
func (s *hostSet) residual() query.Filter {
	if s == nil {
		return nil
	}
	return s.res
}

// hint reports whether a leg should force the store's probe plan over
// the objectId index: a small set is cheaper to verify doc by doc than
// to find through the ANN beam, which may never reach it. Meaningful
// because filterIdsMax < filterResidualMax: the probe candidate exists
// only while the residual yields index bounds.
func (s *hostSet) hint() bool {
	return s != nil && s.small
}

// lazy reports whether the legs post-filter their rows through keep.
func (s *hostSet) lazy() bool {
	return s != nil && s.res == nil
}

// empty reports a filter no object satisfies: nothing can match, so a
// search answers without embedding the query or opening a leg.
func (s *hostSet) empty() bool {
	return s != nil && s.small && len(s.list) == 0
}

// materialize resolves the complete set when it holds at most max
// ids; a larger set stays lazy. A no-op once exact, or when a smaller
// bound already came back short. Each resolve walks the objects
// collection from the top on its own snapshot: a set left lazy at one
// bound is re-read up to the next, and a concurrent object write can
// make it disagree with verdicts already cached — the row is read live,
// and a search tolerates that.
func (s *hostSet) materialize(ctx context.Context, max int) error {
	if s == nil || s.exact || max <= s.triedMax {
		return nil
	}
	ids, more, err := s.filter.Resolve(ctx, max)
	if err != nil {
		return err
	}
	if more {
		s.triedMax = max
		return nil
	}
	s.setExact(ids)
	return nil
}

// keep returns the hits whose object is in the set, in order. Unknown
// objects of a lazy set are resolved in one Match.
func (s *hostSet) keep(ctx context.Context, hits []Hit) ([]Hit, error) {
	if s == nil || len(hits) == 0 {
		return hits, nil
	}
	if !s.exact {
		var ask []string
		for _, h := range hits {
			if _, ok := s.known[h.ObjectId]; !ok {
				s.known[h.ObjectId] = false
				ask = append(ask, h.ObjectId)
			}
		}
		if len(ask) > 0 {
			matched, err := s.filter.Match(ctx, ask)
			if err != nil {
				return nil, err
			}
			// Only the ids asked about, only positive verdicts.
			for _, id := range ask {
				if matched[id] {
					s.known[id] = true
				}
			}
		}
	}
	out := make([]Hit, 0, len(hits))
	for _, h := range hits {
		if s.has(h.ObjectId) {
			out = append(out, h)
		}
	}
	return out, nil
}

func (s *hostSet) has(objectId string) bool {
	if s.exact {
		_, ok := s.ids[objectId]
		return ok
	}
	return s.known[objectId]
}
