package index

import (
	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any/anyuri"
)

// Links — the edges a chunker reports next to a record's text. One
// read of the record feeds both the search sink and the link sink
// (docs/13-index.md § Links).

// Link kinds. An open slug set the extractors mint; these are the
// established vocabulary.
const (
	// LinkKindMention — an identity referenced in text (`m` URI).
	LinkKindMention = "mention"
	// LinkKindLink — an ordinary reference in text: an object, one of
	// its records or property values, a file.
	LinkKindLink = "link"
	// LinkKindRelation — a value of a link-bearing property or dataset
	// field (the `relation` slug, or an explicit `links` marker on a
	// value field).
	LinkKindRelation = "relation"
	// LinkKindCard — an editor block that is exactly one link to an
	// object or file, rendered as a card (the whole-line rule the web
	// client promotes on).
	LinkKindCard = "card"
	// LinkKindEmbed — an editor block that embeds another object's
	// content: the synced-block envelope referencing a source block.
	LinkKindEmbed = "embed"
)

// LinkEntry is one edge: the record it was found in (the source
// place — same identity vocabulary as IndexEntry, `DatasetProp` +
// propId for a property value) and the canonical target.
type LinkEntry struct {
	ObjectId string
	Dataset  string
	RecordId string
	Kind     string
	Target   anyuri.URI // canonical (URI.Canonical), never a space
}

// Link markers on a descriptor: the explicit `links` key, or the
// slug that implies one. What the marker means for the value:
//
//	link      the string value is one reference
//	links     the array value lists references
//	markdown  the text is scanned for references
const (
	XFormatLinksKey = "links"

	LinkModeOne      = "link"
	LinkModeMany     = "links"
	LinkModeMarkdown = "markdown"

	slugRelation = "relation"
	slugMarkdown = "markdown"
)

// LinkMode resolves a descriptor's link marker: the explicit `links`
// key wins whatever it holds (an unknown or malformed value disables —
// the gate refuses those on write, so one only arrives from a vendor
// or a bypass), else the `relation` slug implies `links` and the
// `markdown` slug implies `markdown`. Empty when the field carries no
// links.
func LinkMode(xf map[string]any) string {
	if xf == nil {
		return ""
	}
	if raw, present := xf[XFormatLinksKey]; present {
		m, _ := raw.(string)
		switch m {
		case LinkModeOne, LinkModeMany, LinkModeMarkdown:
			return m
		}
		return ""
	}
	switch xf["type"] {
	case slugRelation:
		return LinkModeMany
	case slugMarkdown:
		return LinkModeMarkdown
	}
	return ""
}

// ValidLinkMode reports whether s is one of the three marker values.
func ValidLinkMode(s string) bool {
	switch s {
	case LinkModeOne, LinkModeMany, LinkModeMarkdown:
		return true
	}
	return false
}

// TextLinks scans markdown text for references and returns the edges
// for source (objectId, dataset, recordId) in spaceId: mentions as
// LinkKindMention, everything else as LinkKindLink. Space references,
// unknown kinds and a link to the source object itself (no record, no
// property — the parent is not a backlink) are dropped; duplicates
// merge on the canonical target.
func TextLinks(spaceId, objectId, dataset, recordId, text string) []LinkEntry {
	var out []LinkEntry
	seen := map[string]bool{}
	for _, u := range anyuri.ExtractLinks(text) {
		c, ok := u.Canonical(spaceId)
		if !ok || isSelf(c, spaceId, objectId) {
			continue
		}
		key := c.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		kind := LinkKindLink
		if c.Kind == anyuri.KindMention {
			kind = LinkKindMention
		}
		out = append(out, LinkEntry{ObjectId: objectId, Dataset: dataset, RecordId: recordId, Kind: kind, Target: c})
	}
	return out
}

// ValueLinks reads references off a value field by its marker mode:
// one string for LinkModeOne, an array of strings for LinkModeMany,
// scanned text for LinkModeMarkdown. Value links are LinkKindRelation;
// text links keep their text kinds. Malformed elements are skipped.
func ValueLinks(spaceId, objectId, dataset, recordId, mode string, v *anyenc.Value) []LinkEntry {
	if v == nil {
		return nil
	}
	switch mode {
	case LinkModeMarkdown:
		if v.Type() != anyenc.TypeString {
			return nil
		}
		return TextLinks(spaceId, objectId, dataset, recordId, string(v.GetStringBytes()))
	case LinkModeOne:
		if v.Type() != anyenc.TypeString {
			return nil
		}
		return refLinks(spaceId, objectId, dataset, recordId, []string{string(v.GetStringBytes())})
	case LinkModeMany:
		var refs []string
		switch v.Type() {
		case anyenc.TypeArray:
			for _, el := range v.GetArray() {
				if el.Type() == anyenc.TypeString {
					refs = append(refs, string(el.GetStringBytes()))
				}
			}
		case anyenc.TypeString:
			refs = []string{string(v.GetStringBytes())}
		}
		return refLinks(spaceId, objectId, dataset, recordId, refs)
	}
	return nil
}

// refLinks parses reference strings (each a whole URI) into relation
// edges, canonical and deduplicated, self dropped.
func refLinks(spaceId, objectId, dataset, recordId string, refs []string) []LinkEntry {
	var out []LinkEntry
	seen := map[string]bool{}
	for _, ref := range refs {
		u, err := anyuri.Parse(ref)
		if err != nil {
			continue
		}
		c, ok := u.Canonical(spaceId)
		if !ok || isSelf(c, spaceId, objectId) {
			continue
		}
		key := c.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, LinkEntry{ObjectId: objectId, Dataset: dataset, RecordId: recordId, Kind: LinkKindRelation, Target: c})
	}
	return out
}

// isSelf reports whether a canonical target is the source object
// itself, as a whole.
func isSelf(c anyuri.URI, spaceId, objectId string) bool {
	return c.Kind == anyuri.KindObject && c.SpaceId == spaceId && c.ObjectId == objectId && c.RecordId == ""
}
