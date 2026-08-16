package server

import (
	"slices"
	"strings"
)

// Event ↔ pub/sub topic mapping (docs/21-events.md § Scopes over the
// network). A dotted type becomes slash segments under the `ev/`
// prefix, with the target always appended as exactly one segment
// (`-` when absent) so an exact-type interest is single-form:
//
//	process.progress + target p1 → ev/process/progress/p1
//	process.progress, no target  → ev/process/progress/-
//
// Self-owned types map into pub/sub's reserved `acc/…/<accountId>`
// namespace instead — only that account can publish there (enforced
// at publisher, relay and receiver), which makes them spoof-proof for
// presence-style signals:
//
//	editor.cursor + target o1 by A → acc/ev/editor/cursor/o1/A
var selfOwnedEventTypes = map[string]bool{
	"editor.cursor": true,
}

// eventTopicPrefix / eventAccTopicPrefix root every bus topic so the
// bus never collides with other pub/sub users in the same space.
const (
	eventTopicPrefix    = "ev/"
	eventAccTopicPrefix = "acc/ev/"
)

// eventTopic renders the publish topic for one event.
func eventTopic(typ, target, accountId string) string {
	segs := strings.ReplaceAll(typ, ".", "/")
	if target == "" {
		target = "-"
	}
	if selfOwnedEventTypes[typ] {
		return eventAccTopicPrefix + segs + "/" + target + "/" + accountId
	}
	return eventTopicPrefix + segs + "/" + target
}

// filterPatterns derives the pub/sub interest patterns for one SSE
// filter. Patterns are a coarse pull filter — the hub re-filters every
// delivery against the full eventFilter, so patterns only need to
// cover (never narrow beyond) what the filter accepts:
//
//   - no type filter → everything: ev/> plus acc/ev/>
//   - prefix "p.*" → ev/p/> (plus the acc twin when the prefix covers
//     a self-owned type)
//   - exact type + targets → one concrete ev/…/<target> per target;
//     without targets → ev/…/* (the target segment is always present)
//   - exact self-owned type → acc/ev/…/> (target + account tail)
//
// The target dimension is ignored for prefix/catch-all patterns (its
// segment position isn't fixed there); the hub filters it locally.
func filterPatterns(f *eventFilter) []string {
	if len(f.types) == 0 {
		return []string{eventTopicPrefix + ">", eventAccTopicPrefix + ">"}
	}
	seen := make(map[string]bool)
	var out []string
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, t := range f.types {
		if prefix, ok := strings.CutSuffix(t, ".*"); ok {
			segs := strings.ReplaceAll(prefix, ".", "/")
			add(eventTopicPrefix + segs + "/>")
			for so := range selfOwnedEventTypes {
				if typeFilterMatches(t, so) {
					add(eventAccTopicPrefix + segs + "/>")
					break
				}
			}
			continue
		}
		segs := strings.ReplaceAll(t, ".", "/")
		if selfOwnedEventTypes[t] {
			add(eventAccTopicPrefix + segs + "/>")
			continue
		}
		if len(f.targets) == 0 {
			add(eventTopicPrefix + segs + "/*")
			continue
		}
		for _, target := range f.targets {
			add(eventTopicPrefix + segs + "/" + target)
		}
	}
	return out
}

// patternSubsumes reports whether pattern a accepts every topic b
// accepts, for the pattern family filterPatterns generates (wildcards
// only as a full final segment: trailing `>` or a final `*`). Within
// that family two patterns either nest or are disjoint, so the
// maximal (non-subsumed) subset of an interest set is pairwise
// disjoint — the property the bridge relies on to never deliver one
// message twice.
func patternSubsumes(a, b string) bool {
	if a == b {
		return true
	}
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	for i, aseg := range as {
		if aseg == ">" {
			// `>` matches one or more remaining segments; b must have
			// at least one left.
			return i < len(bs)
		}
		if i >= len(bs) {
			return false
		}
		switch aseg {
		case "*":
			// exactly one segment; b's `>` (one-or-more) is broader.
			if bs[i] == ">" {
				return false
			}
		default:
			if bs[i] != aseg {
				return false
			}
		}
	}
	return len(bs) == len(as)
}

// maximalPatterns filters set down to the patterns not subsumed by
// another member — the pairwise-disjoint cover the bridge actually
// subscribes. Deterministic order for tests.
func maximalPatterns(set map[string]int) []string {
	var out []string
	for p := range set {
		covered := false
		for q := range set {
			if q != p && patternSubsumes(q, p) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}
