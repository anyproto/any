package indexer

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	anystore "github.com/anyproto/any-store/v2"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/index"
)

// The title pass is any-store's search-as-you-type recipe
// (docs/full-text-search.md § Search-as-you-type in the any-store
// module), run at the head of every hybrid / fts search and alone in
// title mode (docs/13-index.md § Search). The typed string is split by
// the index's own analyzer and matched against object names — the
// nameField of the name docs — in tiers, most specific first; the last
// word is a prefix unless the query ends in whitespace. The queries are
// built from analyzed terms, so typed punctuation never acts as an
// operator, and no stop word is stripped: a title keeps its "the".

// titleTiers returns the analyzed terms of typed and the $search string
// of each tier: ^"terms"* (the name starts with the typed words),
// "terms"* (they appear in order inside it) and, for two or more words,
// every word as its own term with the last a prefix (all present, any
// order). A quote inside a term is doubled, as the parser expects. Nil
// terms = nothing to match.
func titleTiers(typed string) (terms []string, tiers []string) {
	terms = anystore.FtsAnalyzeTerms(typed)
	if len(terms) == 0 {
		return nil, nil
	}
	star := "*"
	if r, _ := utf8.DecodeLastRuneInString(typed); unicode.IsSpace(r) {
		star = ""
	}
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	phrase := quote(strings.Join(terms, " "))
	tiers = []string{"^" + phrase + star, phrase + star}
	if len(terms) > 1 {
		words := make([]string, len(terms))
		for i, t := range terms {
			words[i] = quote(t)
		}
		words[len(words)-1] += star
		tiers = append(tiers, strings.Join(words, " "))
	}
	return terms, tiers
}

// titleMatch names the tier a title hit came from. The anchored tier
// splits in two: exact when the name's analyzed terms are the typed
// terms, prefix otherwise.
func titleMatch(tier int, name string, terms []string) string {
	switch tier {
	case 0:
		if slices.Equal(anystore.FtsAnalyzeTerms(name), terms) {
			return api.SearchMatchExact
		}
		return api.SearchMatchPrefix
	case 1:
		return api.SearchMatchPhrase
	default:
		return api.SearchMatchWords
	}
}

// matchRank orders the tiers for the reply.
var matchRank = map[string]int{
	api.SearchMatchExact:  0,
	api.SearchMatchPrefix: 1,
	api.SearchMatchPhrase: 2,
	api.SearchMatchWords:  3,
}

// titlesInScope reports whether a request's scopes admit the name docs
// (index.ScopeBasic); a search scoped elsewhere runs no title pass.
func titlesInScope(scopes []string) bool {
	return len(scopes) == 0 || slices.Contains(scopes, index.ScopeBasic)
}

// titlePass runs the tiers until limit objects are collected: one
// lexical cursor per tier, scoped to nameField with every typed word
// required and the request's require / exclude / filter applied, later
// tiers skipped once the page is full. Hits come back in tier order —
// exact names first within the anchored tier, then the store's BM25
// order, which with a title-length doc is the shortest names first —
// one per object, Match set. A tier reads limit distinct records: the
// tiers nest, so a later one repeats at most the objects already shown
// and still yields the rest of the page.
func (ix *Indexer) titlePass(ctx context.Context, spaceId string, req api.SearchRequest, limit int, hs *hostSet) (hits []Hit, budgetOut bool, err error) {
	terms, tiers := titleTiers(req.Query)
	if len(tiers) == 0 {
		return nil, false, nil
	}
	seen := make(map[string]struct{}, limit)
	for tier, search := range tiers {
		if len(hits) >= limit {
			break
		}
		fq := FTSQuery{
			Query:      search,
			DefaultAnd: true,
			Require:    req.Require,
			Exclude:    req.Exclude,
			Fields:     []string{nameField},
		}
		got, out, err := ix.ftsLeg(ctx, spaceId, fq, nil, legCover{fetch: limit, groups: limit}, hs)
		if err != nil {
			return nil, false, err
		}
		budgetOut = budgetOut || out
		for _, h := range got {
			if _, dup := seen[h.ObjectId]; dup {
				continue
			}
			seen[h.ObjectId] = struct{}{}
			h.Match = titleMatch(tier, h.Data, terms)
			hits = append(hits, h)
			if len(hits) >= limit {
				break
			}
		}
	}
	slices.SortStableFunc(hits, func(a, b Hit) int {
		return cmp.Compare(matchRank[a.Match], matchRank[b.Match])
	})
	return hits, budgetOut, nil
}

// withoutDocs drops from hits the docs already shown as titles — the
// content legs match every field, so a name doc can come back a second
// time through them.
func withoutDocs(hits, shown []Hit) []Hit {
	if len(shown) == 0 || len(hits) == 0 {
		return hits
	}
	ids := make(map[string]struct{}, len(shown))
	for _, h := range shown {
		ids[hitDocId(h)] = struct{}{}
	}
	out := hits[:0]
	for _, h := range hits {
		if _, dup := ids[hitDocId(h)]; !dup {
			out = append(out, h)
		}
	}
	return out
}
