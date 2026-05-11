package markdown

import (
	"strings"

	"github.com/anyproto/any/internal/editor"
)

// ParsedBlock is the typed view of one raw markdown block produced by
// Split. It mirrors the on-the-wire shape of a body_blocks record
// (minus the SDK-managed id and _ver) so the markdown.Set path can
// diff and emit ops directly.
type ParsedBlock struct {
	Type  string
	Style map[string]any // nil when no style metadata applies
	Text  string         // inline markdown only (no block-level syntax)
}

// ParseBlock classifies a raw block from Split into a typed
// ParsedBlock. The classification mirrors the splitter's block
// recognisers — same checks, just lifted into structured form.
func ParseBlock(raw string) ParsedBlock {
	if raw == "" {
		return ParsedBlock{Type: editor.TypeParagraph}
	}
	lines := strings.Split(raw, "\n")

	if len(lines) == 1 && isDividerLine(lines[0]) {
		return ParsedBlock{Type: editor.TypeDivider}
	}

	if level, text, ok := parseATXHeading(lines[0]); ok && len(lines) == 1 {
		return ParsedBlock{
			Type:  editor.TypeHeading,
			Style: map[string]any{editor.StyleLevel: level},
			Text:  text,
		}
	}

	if len(lines) == 2 {
		if level, ok := parseSetextUnderline(lines[1]); ok && !looksLikeAnyOpener(lines[0]) {
			return ParsedBlock{
				Type:  editor.TypeHeading,
				Style: map[string]any{editor.StyleLevel: level},
				Text:  strings.TrimSpace(lines[0]),
			}
		}
	}

	if isFenceOpen(lines[0]) {
		lang, body := parseFencedCode(lines)
		style := map[string]any{}
		if lang != "" {
			style[editor.StyleLang] = lang
		}
		var stylePtr map[string]any
		if len(style) > 0 {
			stylePtr = style
		}
		return ParsedBlock{
			Type:  editor.TypeCode,
			Style: stylePtr,
			Text:  body,
		}
	}

	if isBlockquoteLine(lines[0]) {
		return ParsedBlock{
			Type: editor.TypeQuote,
			Text: stripQuotePrefixes(lines),
		}
	}

	if checked, text, ok := parseCheckListItem(lines[0]); ok {
		return ParsedBlock{
			Type:  editor.TypeCheckListItem,
			Style: map[string]any{editor.StyleChecked: checked},
			Text:  joinListBody(text, lines[1:]),
		}
	}
	if ordered, text, ok := parseListItem(lines[0]); ok {
		return ParsedBlock{
			Type:  editor.TypeListItem,
			Style: map[string]any{editor.StyleOrdered: ordered},
			Text:  joinListBody(text, lines[1:]),
		}
	}

	if isHTMLBlockLine(lines[0]) {
		return ParsedBlock{Type: editor.TypeHTML, Text: raw}
	}
	if isTableLine(lines[0]) {
		return ParsedBlock{Type: editor.TypeTable, Text: raw}
	}
	return ParsedBlock{Type: editor.TypeParagraph, Text: raw}
}

// RenderBlock produces the canonical markdown bytes for a ParsedBlock.
// Inverse of ParseBlock for the canonical-output case — input
// `"+ foo"` parses as `{list_item}` and renders as `"- foo"`; setext
// headings parse as `{heading, level}` and render as ATX. Round-trip
// equivalence holds for the common cases used by the e2e suite
// (paragraphs and ATX headings).
func RenderBlock(b ParsedBlock) string {
	switch b.Type {
	case editor.TypeHeading:
		level := intStyle(b.Style, editor.StyleLevel, 1)
		if level < 1 {
			level = 1
		}
		if level > 6 {
			level = 6
		}
		return strings.Repeat("#", level) + " " + b.Text
	case editor.TypeListItem:
		marker := "-"
		if boolStyle(b.Style, editor.StyleOrdered, false) {
			marker = "1."
		}
		return marker + " " + b.Text
	case editor.TypeCheckListItem:
		marker := "- [ ]"
		if boolStyle(b.Style, editor.StyleChecked, false) {
			marker = "- [x]"
		}
		return marker + " " + b.Text
	case editor.TypeCode:
		lang := stringStyle(b.Style, editor.StyleLang, "")
		body := b.Text
		if body == "" {
			return "```" + lang + "\n```"
		}
		return "```" + lang + "\n" + body + "\n```"
	case editor.TypeQuote:
		return prefixLines(b.Text, "> ")
	case editor.TypeDivider:
		return "---"
	case editor.TypeHTML, editor.TypeTable:
		return b.Text
	case editor.TypeImage:
		// v1: image blocks render to their text payload (caller
		// supplied "![alt](url)"). File upload is a tracked gap.
		return b.Text
	default:
		return b.Text
	}
}

// --- block-recogniser helpers ---------------------------------------

func parseATXHeading(line string) (level int, text string, ok bool) {
	t := trimLeadingSpaces(line, 3)
	if len(t) == 0 || t[0] != '#' {
		return 0, "", false
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n < 1 || n > 6 {
		return 0, "", false
	}
	if n == len(t) {
		return n, "", true
	}
	if t[n] != ' ' && t[n] != '\t' {
		return 0, "", false
	}
	body := strings.TrimSpace(t[n:])
	body = strings.TrimRight(body, "# \t") // ATX closing #s are optional / cosmetic
	return n, body, true
}

func parseSetextUnderline(line string) (level int, ok bool) {
	t := strings.TrimRight(trimLeadingSpaces(line, 3), " \t")
	if len(t) == 0 {
		return 0, false
	}
	switch t[0] {
	case '=':
		for i := 0; i < len(t); i++ {
			if t[i] != '=' {
				return 0, false
			}
		}
		return 1, true
	case '-':
		for i := 0; i < len(t); i++ {
			if t[i] != '-' {
				return 0, false
			}
		}
		return 2, true
	}
	return 0, false
}

func parseFencedCode(lines []string) (lang, body string) {
	if len(lines) == 0 {
		return "", ""
	}
	t := trimLeadingSpaces(lines[0], 3)
	// Skip the fence chars on the opener.
	c := t[0]
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	lang = strings.TrimSpace(t[n:])

	// Skip a closing fence on the final line if present.
	end := len(lines)
	if end >= 2 {
		last := strings.TrimRight(trimLeadingSpaces(lines[end-1], 3), " \t")
		if len(last) >= 3 && allByte(last, c) {
			end--
		}
	}
	if end <= 1 {
		return lang, ""
	}
	body = strings.Join(lines[1:end], "\n")
	return lang, body
}

func parseListItem(line string) (ordered bool, text string, ok bool) {
	cont, m := listItemMarker(line)
	if !m {
		return false, "", false
	}
	body := strings.TrimSpace(line[cont:])
	// Marker is at indent[0]; ordered iff the first non-space byte is a
	// digit.
	leading := 0
	for leading < len(line) && (line[leading] == ' ' || line[leading] == '\t') {
		leading++
	}
	if leading >= len(line) {
		return false, body, true
	}
	c := line[leading]
	ordered = c >= '0' && c <= '9'
	return ordered, body, true
}

// parseCheckListItem matches `- [ ] x` / `- [x] x`. Runs before
// parseListItem; on no-match the caller falls through to plain
// list-item handling.
func parseCheckListItem(line string) (checked bool, text string, ok bool) {
	cont, m := listItemMarker(line)
	if !m {
		return false, "", false
	}
	rest := line[cont:]
	if len(rest) < 3 || rest[0] != '[' || rest[2] != ']' {
		return false, "", false
	}
	switch rest[1] {
	case ' ':
		checked = false
	case 'x', 'X':
		checked = true
	default:
		return false, "", false
	}
	body := strings.TrimSpace(rest[3:])
	return checked, body, true
}

// joinListBody glues the first-line body (already stripped of marker)
// with any continuation lines following the marker line. Continuation
// lines are emitted verbatim — preserves the user's indentation so
// re-parsing recognises them as continuations.
func joinListBody(firstLine string, rest []string) string {
	if len(rest) == 0 {
		return firstLine
	}
	return firstLine + "\n" + strings.Join(rest, "\n")
}

func isDividerLine(line string) bool {
	t := strings.TrimRight(trimLeadingSpaces(line, 3), " \t")
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '_' && c != '*' {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] != c && t[i] != ' ' {
			return false
		}
	}
	return true
}

func stripQuotePrefixes(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		t := trimLeadingSpaces(l, 3)
		if len(t) > 0 && t[0] == '>' {
			body := t[1:]
			if len(body) > 0 && (body[0] == ' ' || body[0] == '\t') {
				body = body[1:]
			}
			out[i] = body
		} else {
			out[i] = l
		}
	}
	return strings.Join(out, "\n")
}

func prefixLines(text, prefix string) string {
	if text == "" {
		return strings.TrimRight(prefix, " ")
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = strings.TrimRight(prefix, " ")
		} else {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

func intStyle(style map[string]any, key string, def int) int {
	if style == nil {
		return def
	}
	switch v := style[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	}
	return def
}

func boolStyle(style map[string]any, key string, def bool) bool {
	if style == nil {
		return def
	}
	if v, ok := style[key].(bool); ok {
		return v
	}
	return def
}

func stringStyle(style map[string]any, key, def string) string {
	if style == nil {
		return def
	}
	if v, ok := style[key].(string); ok {
		return v
	}
	return def
}
