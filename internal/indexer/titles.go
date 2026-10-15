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

// titleTier is one tier of the pass: its $search string and the match
// it yields. The exact tier keeps only the names whose analyzed terms
// are the typed terms — the engine has no equality operator, so it is
// the anchored whole-word phrase filtered by token count.
type titleTier struct {
	search string
	match  string
}

// titleTiers returns the analyzed terms of typed and the tiers:
// ^"terms" filtered to exact names; ^"terms"* (the name starts with the
// typed words); "terms"* (they appear in order inside it); and, for two
// or more words, every word as its own term with the last a prefix (all
// present, any order). A quote inside a term is doubled, as the parser
// expects. Nil terms = nothing to match.
func titleTiers(typed string) (terms []string, tiers []titleTier) {
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
	tiers = []titleTier{
		{search: "^" + phrase, match: api.SearchMatchExact},
		{search: "^" + phrase + star, match: api.SearchMatchPrefix},
		{search: phrase + star, match: api.SearchMatchPhrase},
	}
	if len(terms) > 1 {
		words := make([]string, len(terms))
		for i, t := range terms {
			words[i] = quote(t)
		}
		words[len(words)-1] += star
		tiers = append(tiers, titleTier{search: strings.Join(words, " "), match: api.SearchMatchWords})
	}
	return terms, tiers
}

// titlesInScope reports whether a request's scopes admit the name docs
// (index.ScopeBasic); a search scoped elsewhere runs no title pass.
func titlesInScope(scopes []string) bool {
	return len(scopes) == 0 || slices.Contains(scopes, index.ScopeBasic)
}

// titlePass runs the tiers until limit objects are collected: one
// lexical cursor per tier, scoped to nameField with every typed word
// required and the request's require / exclude / filter applied, later
// tiers skipped once the page is full. Hits come back in tier order,
// one per object, Match set; within a tier the store's BM25 order
// stands — an anchored or phrase clause scores one occurrence, so that
// is the shortest names first — and names of equal score sort by their
// folded text, so a page is the same on every device whatever the
// insertion order. A tier reads limit distinct records: the tiers nest,
// so a later one repeats at most the objects already shown and still
// yields the rest of the page. Only chunk 0 of a name carries
// nameField, so a hit is the name's head and a long name is one hit.
func (ix *Indexer) titlePass(ctx context.Context, spaceId string, req api.SearchRequest, limit int, hs *hostSet) (hits []Hit, budgetOut bool, err error) {
	terms, tiers := titleTiers(req.Query)
	if len(tiers) == 0 {
		return nil, false, nil
	}
	seen := make(map[string]struct{}, limit)
	for _, tier := range tiers {
		if len(hits) >= limit {
			break
		}
		fq := FTSQuery{
			Query:      tier.search,
			DefaultAnd: true,
			Require:    req.Require,
			Exclude:    req.Exclude,
			Fields:     []string{nameField},
		}
		got, out, err := ix.ftsLeg(ctx, spaceId, fq, nil, legCover{fetch: limit, groups: limit}, hs, nil)
		if err != nil {
			return nil, false, err
		}
		budgetOut = budgetOut || out
		if tier.match == api.SearchMatchExact {
			got = slices.DeleteFunc(got, func(h Hit) bool {
				return !slices.Equal(anystore.FtsAnalyzeTerms(h.Data), terms)
			})
		}
		slices.SortStableFunc(got, func(a, b Hit) int {
			return cmp.Or(cmp.Compare(b.Score, a.Score), strings.Compare(foldName(a.Data), foldName(b.Data)))
		})
		for _, h := range got {
			if _, dup := seen[h.ObjectId]; dup {
				continue
			}
			seen[h.ObjectId] = struct{}{}
			h.Match = tier.match
			hits = append(hits, h)
			if len(hits) >= limit {
				break
			}
		}
	}
	return hits, budgetOut, nil
}

// foldName is the tie-break key of a title hit: the name case-folded.
func foldName(s string) string {
	return strings.Map(unicode.ToLower, s)
}

// shownRecords keys the title hits by record (groupKey): the content
// legs match every field, so a name doc — any chunk of it — can come
// back a second time through them.
func shownRecords(titles []Hit) map[string]struct{} {
	if len(titles) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(titles))
	for _, h := range titles {
		out[groupKey(h)] = struct{}{}
	}
	return out
}
