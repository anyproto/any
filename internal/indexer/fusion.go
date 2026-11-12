package indexer

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// fusionPlan is one query's RRF form: per-leg weights (0 drops the leg)
// and the damping constant k.
type fusionPlan struct {
	fts, vec float64
	k        int
}

// The fusion tree's cuts: a depth-2 tree learned over leg features on
// lookups, paraphrases and questions over notes, chats and public sets.
// Every branch is weighted RRF, so a normalized score means the same on
// every branch.
const (
	treeCoverage = 0.8   // share of the query's words in the best top-3 FTS chunk
	treeVecGap   = 0.103 // cosine top-1 − top-5; below it the vector leg is flat
	treeFtsGap   = 0.184 // BM25 (top-1 − top-2) / top-1; below it no lexical winner
)

// planFusion picks a query's RRF form from its legs. Low lexical
// coverage: a peaked vector leg decides alone, a flat one fuses
// vector-heavy. High coverage: a peaked BM25 leg decides alone, a flat
// one fuses lexical-heavy. A single leg fuses at k 0: the order is the
// same for any k, and its normalized score 1/rank (ranks from 1) lets a
// gate above 0.5 admit the first hit only — the one reliably relevant
// single-leg hit. The other leg's hits follow it (backfill).
func planFusion(query string, fts, vec []Hit) fusionPlan {
	if queryCoverage(query, fts) < treeCoverage {
		if vectorGap(vec) < treeVecGap {
			return fusionPlan{fts: 1, vec: 2, k: 20}
		}
		return fusionPlan{vec: 1}
	}
	if lexicalGap(fts) < treeFtsGap {
		return fusionPlan{fts: 1, vec: 0.5, k: 20}
	}
	return fusionPlan{fts: 1}
}

// coverageStop is the stop list the tree was learned with; it is not the
// FTS leg's stopWords.
var coverageStop = map[string]struct{}{}

func init() {
	for _, w := range strings.Fields("a an the of to in on for and or is are was were be by with at from as it this that what how why when which who do does did i my me we our you your") {
		coverageStop[w] = struct{}{}
	}
}

// coverageSep keeps combining marks inside a word: a vowel or tone sign
// does not end it.
func coverageSep(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) && r != '_'
}

// coverageFold is s in the form coverage compares: NFKC, lowercase.
func coverageFold(s string) string {
	return strings.ToLower(norm.NFKC.String(s))
}

// coverageWords is a folded text's word set (letters, marks, digits, '_').
func coverageWords(folded string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range strings.FieldsFunc(folded, coverageSep) {
		out[w] = struct{}{}
	}
	return out
}

// queryWords is the query's word set minus coverageStop; the value marks
// a word typed as a prefix (`word*`, `"a phrase"*`), which any word
// starting with it covers and which is never a stop word. A `*` field
// with no word of its own (`"a phrase "*`) marks the word before it.
func queryWords(query string) map[string]bool {
	out := map[string]bool{}
	last := ""
	for _, f := range strings.Fields(coverageFold(query)) {
		prefix := strings.HasSuffix(f, "*")
		words := strings.FieldsFunc(f, coverageSep)
		if prefix && len(words) == 0 && last != "" {
			out[last] = true
		}
		for i, w := range words {
			last = w
			isPrefix := prefix && i == len(words)-1
			if _, skip := coverageStop[w]; skip && !isPrefix {
				continue
			}
			out[w] = out[w] || isPrefix
		}
	}
	return out
}

// unsegmented reports whether w is written in a script without word
// spaces. Such a word is a whole run of text, so a chunk covers it by
// containing it, not by holding an equal word.
func unsegmented(w string) bool {
	for _, r := range w {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul, unicode.Thai) {
			return true
		}
	}
	return false
}

// queryCoverage is the largest share of the query's words found in one
// of the top three FTS chunks (their full text); 0 for a query with no
// words or no FTS hit.
func queryCoverage(query string, fts []Hit) float64 {
	qw := queryWords(query)
	if len(qw) == 0 {
		return 0
	}
	var best float64
	for _, h := range fts[:min(3, len(fts))] {
		folded := coverageFold(h.Data)
		dw := coverageWords(folded)
		n := 0
		for w, prefix := range qw {
			_, ok := dw[w]
			switch {
			case ok:
			case unsegmented(w):
				ok = strings.Contains(folded, w)
			case prefix:
				ok = hasPrefixWord(dw, w)
			}
			if ok {
				n++
			}
		}
		best = max(best, float64(n)/float64(len(qw)))
	}
	return best
}

func hasPrefixWord(words map[string]struct{}, prefix string) bool {
	for w := range words {
		if strings.HasPrefix(w, prefix) {
			return true
		}
	}
	return false
}

// vectorGap is the cosine drop from the vector leg's first to its fifth
// chunk (or its last, when shorter).
func vectorGap(vec []Hit) float64 {
	if len(vec) == 0 {
		return 0
	}
	return vec[0].Score - vec[min(4, len(vec)-1)].Score
}

// lexicalGap is the FTS leg's relative BM25 drop from first to second
// chunk: 1 for a single hit or a non-positive top score, 0 for none.
func lexicalGap(fts []Hit) float64 {
	switch {
	case len(fts) == 0:
		return 0
	case len(fts) == 1, fts[0].Score <= 0:
		return 1
	}
	return (fts[0].Score - fts[1].Score) / fts[0].Score
}

// lists returns the legs the plan keeps, with their weights.
func (p fusionPlan) lists(fts, vec []Hit) ([][]Hit, []float64) {
	var lists [][]Hit
	var weights []float64
	if p.fts > 0 {
		lists, weights = append(lists, fts), append(weights, p.fts)
	}
	if p.vec > 0 {
		lists, weights = append(lists, vec), append(weights, p.vec)
	}
	return lists, weights
}

// backfill appends to a single leg's fused hits the hits only the other
// leg holds, in that leg's order and scored as the ranks that follow, so
// a leg that decides alone never shortens the page. Both legs fused:
// nothing to add.
func (p fusionPlan) backfill(fused, fts, vec []Hit) []Hit {
	var rest []Hit
	switch {
	case p.fts > 0 && p.vec > 0:
		return fused
	case p.fts > 0:
		rest = vec
	default:
		rest = fts
	}
	seen := make(map[string]struct{}, len(fused))
	for _, h := range fused {
		seen[hitDocId(h)] = struct{}{}
	}
	for _, h := range rest {
		id := hitDocId(h)
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		h.Score = 1 / float64(len(fused)+1)
		fused = append(fused, h)
	}
	return fused
}

// fusionPlan is the query's RRF form under these options: planFusion,
// unless a configured weight or AdaptiveWeights fixes weighted RRF at
// rrfK. fetch is the over-fetch window AdaptiveWeights judges the FTS
// leg on.
func (o Options) fusionPlan(query string, fts, vec []Hit, fetch int) fusionPlan {
	if o.FtsWeight <= 0 && o.VectorWeight <= 0 && !o.AdaptiveWeights {
		return planFusion(query, fts, vec)
	}
	plan := fusionPlan{fts: legWeight([]float64{o.FtsWeight}, 0), vec: legWeight([]float64{o.VectorWeight}, 0), k: rrfK}
	if o.AdaptiveWeights {
		// Down-weight a flat/weak BM25 leg. Judged on the fixed
		// over-fetch window, not the whole pull: the pull's depth
		// follows how records are chunked, and a deeper tail only
		// ever raises the confidence.
		plan.fts = legWeight([]float64{plan.fts * legConfidence(fts[:min(len(fts), fetch)])}, 0)
	}
	return plan
}
