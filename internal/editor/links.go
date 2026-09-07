package editor

import (
	"encoding/json"
	"strings"

	"github.com/anyproto/any/anyuri"
	"github.com/anyproto/any/internal/index"
)

// Block link extraction — what a block contributes to the link index
// (docs/13-index.md § Links).
//
// Every any:// reference in a block's inline markdown is an edge:
// mentions as `mention`, the rest as `link`. Two block shapes carry a
// more specific kind, both recognised from the record alone because
// the web client writes them as plain markdown:
//
//   - card — a paragraph whose entire text is exactly one markdown
//     link to an object or a file (`[Name](any://o/…)`, `[report.pdf]
//     (any://f/…)`). The whole-line rule is the client's own promotion
//     rule; the same link inside a sentence is an ordinary `link`.
//   - embed — the synced-block envelope: an `html` block holding
//     `<!-- any:block {…,"kind":"synced-block","data":{"role":
//     "reference","ref":"any://o/…/editor_blocks/…"}} -->`. The
//     reference names the source block; the transcluded snapshot
//     follows in ordinary records (the client's extension-block
//     contract).

const (
	envelopeOpen  = "<!-- any:block "
	envelopeClose = "-->"
	syncedBlock   = "synced-block"
)

// BlockLinks extracts one block's edges.
func BlockLinks(spaceId, objectId, collection string, b Block) []index.LinkEntry {
	if b.Type == TypeHTML {
		if e, ok := embedLink(spaceId, objectId, collection, b); ok {
			return []index.LinkEntry{e}
		}
	}
	out := index.TextLinks(spaceId, objectId, collection, b.Id, b.Text)
	if b.Type == TypeParagraph && len(out) == 1 && isWholeLineLink(b.Text) {
		switch out[0].Target.Kind {
		case anyuri.KindObject, anyuri.KindFile:
			out[0].Kind = index.LinkKindCard
		}
	}
	return out
}

// isWholeLineLink reports whether text is exactly one markdown link
// `[label](destination)` with nothing else on the line — the client's
// card rule. Escaped brackets inside the label are allowed.
func isWholeLineLink(text string) bool {
	t := strings.TrimSpace(text)
	if len(t) < 5 || t[0] != '[' || t[len(t)-1] != ')' {
		return false
	}
	// Find the label's closing bracket, skipping escapes.
	i := 1
	for i < len(t) {
		switch t[i] {
		case '\\':
			i += 2
			continue
		case ']':
			goto labelEnd
		}
		i++
	}
	return false
labelEnd:
	if i+1 >= len(t) || t[i+1] != '(' {
		return false
	}
	dest := t[i+2 : len(t)-1]
	return dest != "" && !strings.ContainsAny(dest, " \t()")
}

// embedLink recognises the synced-block reference envelope on an html
// block and returns the embed edge to the source block.
func embedLink(spaceId, objectId, collection string, b Block) (index.LinkEntry, bool) {
	t := strings.TrimSpace(b.Text)
	if !strings.HasPrefix(t, envelopeOpen) || !strings.HasSuffix(t, envelopeClose) {
		return index.LinkEntry{}, false
	}
	raw := strings.TrimSpace(t[len(envelopeOpen) : len(t)-len(envelopeClose)])
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Role string `json:"role"`
			Ref  string `json:"ref"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &env); err != nil || env.Kind != syncedBlock || env.Data.Role != "reference" || env.Data.Ref == "" {
		return index.LinkEntry{}, false
	}
	u, err := anyuri.Parse(env.Data.Ref)
	if err != nil {
		return index.LinkEntry{}, false
	}
	c, ok := u.Canonical(spaceId)
	if !ok {
		return index.LinkEntry{}, false
	}
	return index.LinkEntry{ObjectId: objectId, Dataset: collection, RecordId: b.Id, Kind: index.LinkKindEmbed, Target: c}, true
}
