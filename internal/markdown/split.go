package markdown

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anyproto/any/internal/editor"
)

// Split breaks raw markdown into block-records.
//
// A "block" is one of:
//
//   - Front matter: line 1 exactly `---`, YAML-shaped lines (fmScan),
//     a line exactly `---` or `...`; kept verbatim, at the very start of
//     a document only. Otherwise those lines read as the blocks below.
//   - ATX heading (`# foo` … `###### foo`) — single line.
//   - Setext heading (text line followed by `===` or `---` underline).
//   - Fenced code block (``` … ``` or ~~~ … ~~~), opening fence to
//     matching closing fence inclusive. Content kept verbatim.
//   - Blockquote: any run of consecutive `>`-prefixed lines, including
//     blank `>` lines that only have the marker. The whole run is one
//     block.
//   - List item (`-`, `*`, `+`, `\d+.`, `\d+)`): each item is its own
//     block. Continuation/lazy lines indented under the marker are
//     part of the same item.
//   - HTML block (line starting with `<`): consecutive non-blank lines.
//   - Table (line starting with `|`): consecutive non-blank lines.
//   - Paragraph: anything else, until a blank line or a different
//     block opens.
//   - Empty paragraph: a blank line not acting as a block separator,
//     returned as an empty entry.
//
// One blank line between two content blocks is the plain separator;
// every blank line beyond it is one empty paragraph. A run at either
// edge of the document has no separator duty, so all of its lines are
// empty paragraphs — including a document that is blank throughout.
// splitLines still treats a single trailing newline as a terminator,
// so content that merely ends in "\n" gains nothing.
//
// The returned strings carry no trailing newline. Join reconstructs a
// canonical markdown document; round-trip is idempotent —
// Split(Join(Split(x))) == Split(x).
func Split(md string) []string {
	blocks, _ := splitDocument(md)
	return blocks
}

// splitDocument is Split that also reports whether the first block is
// front matter.
func splitDocument(md string) ([]string, bool) {
	return split(md, true)
}

// splitFragment is Split for content that does not start a document
// (an append): it has no front matter.
func splitFragment(md string) []string {
	blocks, _ := split(md, false)
	return blocks
}

func split(md string, document bool) (out []string, frontMatter bool) {
	if md == "" {
		return nil, false
	}
	lines := splitLines(md)
	// blanks counts the current run of blank lines; sawContent says
	// whether a content block precedes it, which is what decides
	// whether one of those lines is spent on separation.
	blanks := 0
	sawContent := false
	i := 0
	if document {
		if end, ok := frontMatterEnd(lines); ok {
			out = append(out, strings.Join(lines[:end], "\n"))
			sawContent = true
			frontMatter = true
			i = end
		}
	}
	for i < len(lines) {
		if isBlank(lines[i]) {
			blanks++
			i++
			continue
		}
		out = appendEmpties(out, blanks, sawContent)
		blanks = 0
		sawContent = true

		switch {
		case isFenceOpen(lines[i]):
			block, next := consumeFence(lines, i)
			out = append(out, block)
			i = next
		case isATXHeading(lines[i]):
			out = append(out, lines[i])
			i++
		case i+1 < len(lines) && isSetextUnderline(lines[i+1]) && !looksLikeAnyOpener(lines[i]):
			out = append(out, lines[i]+"\n"+lines[i+1])
			i += 2
		case isBlockquoteLine(lines[i]):
			block, next := consumeBlockquote(lines, i)
			out = append(out, block)
			i = next
		case isListItemLine(lines[i]):
			block, next := consumeListItem(lines, i)
			out = append(out, block)
			i = next
		case isHTMLBlockLine(lines[i]):
			block, next := consumeUntilBlank(lines, i)
			out = append(out, block)
			i = next
		case isTableLine(lines[i]):
			block, next := consumeUntilBlank(lines, i)
			out = append(out, block)
			i = next
		default:
			block, next := consumeParagraph(lines, i)
			out = append(out, block)
			i = next
		}
	}
	// A trailing run has no following block to separate from, so every
	// line of it is an empty paragraph — same rule as a leading run.
	return appendEmpties(out, blanks, false), frontMatter
}

// appendEmpties appends the empty entries a blank run of n lines
// stands for: one line pays for the separator when a content block
// precedes the run, none does at either edge.
func appendEmpties(out []string, n int, afterContent bool) []string {
	if afterContent {
		n--
	}
	for ; n > 0; n-- {
		out = append(out, "")
	}
	return out
}

// splitLines splits on "\n", strips a trailing CR so "\r\n" inputs
// work the same as "\n", and treats a single trailing empty line
// (from input that ended in "\n") as a terminator rather than an
// extra blank — otherwise unterminated fences and trailing
// paragraphs gain a phantom empty line on read-back.
func splitLines(s string) []string {
	out := strings.Split(s, "\n")
	for i, l := range out {
		if strings.HasSuffix(l, "\r") {
			out[i] = l[:len(l)-1]
		}
	}
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func isBlank(l string) bool { return strings.TrimSpace(l) == "" }

// frontMatterEnd reports whether lines open with front matter — line 1
// exactly `---`, then lines fmScan admits up to a closer — and the index
// of the first line after the closer.
func frontMatterEnd(lines []string) (int, bool) {
	if len(lines) < 2 || lines[0] != "---" {
		return 0, false
	}
	var scan fmScan
	for j := 1; j < len(lines); j++ {
		switch scan.step(lines[j]) {
		case fmClosed:
			return j + 1, true
		case fmNone:
			return 0, false
		}
	}
	return 0, false
}

func isFrontMatterCloser(l string) bool { return l == "---" || l == "..." }

type fmState int

const (
	fmOpen   fmState = iota // the line belongs to the front matter
	fmClosed                // the line closes it
	fmNone                  // the line makes it markdown
)

// fmScan walks the lines after an opening `---`. Every line before the
// closer is blank or YAML-shaped (yamlLine), the first a key line, and
// the text between the delimiters fits one block.
type fmScan struct {
	lines, size int
}

func (s *fmScan) step(l string) fmState {
	l = strings.TrimSuffix(l, "\r")
	switch {
	case isFrontMatterCloser(l):
		return fmClosed
	case isFenceOpen(l):
		return fmNone
	case s.lines == 0 && !yamlKeyLine(l):
		return fmNone
	case !isBlank(l) && !yamlLine(l):
		return fmNone
	}
	if s.lines > 0 {
		s.size++
	}
	s.lines++
	s.size += len(l)
	if s.size > editor.MaxTextBytes {
		return fmNone
	}
	return fmOpen
}

// yamlLine: a key line, a `- ` list item, or an indented line.
func yamlLine(l string) bool {
	return yamlKeyLine(l) || strings.HasPrefix(l, "- ") || l[0] == ' ' || l[0] == '\t'
}

// yamlKeyLine: a bare key — letters, digits and `_`, then also `.` and
// `-`, no spaces — or a quoted one, then `:` and a space, tab or end.
func yamlKeyLine(l string) bool {
	var rest string
	switch {
	case l == "":
		return false
	case l[0] == '"' || l[0] == '\'':
		end := strings.IndexByte(l[1:], l[0])
		if end < 0 {
			return false
		}
		rest = l[end+2:]
	default:
		n := 0
		for n < len(l) {
			r, size := utf8.DecodeRuneInString(l[n:])
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && (n == 0 || (r != '.' && r != '-')) {
				break
			}
			n += size
		}
		if n == 0 {
			return false
		}
		rest = l[n:]
	}
	return rest == ":" || strings.HasPrefix(rest, ": ") || strings.HasPrefix(rest, ":\t")
}

// looksLikeAnyOpener reports whether a line starts a non-paragraph
// block — used to gate the Setext underline lookahead so a heading
// underline cannot turn a blockquote / list item into a setext
// heading.
func looksLikeAnyOpener(l string) bool {
	return isFenceOpen(l) ||
		isATXHeading(l) ||
		isBlockquoteLine(l) ||
		isListItemLine(l) ||
		isHTMLBlockLine(l) ||
		isTableLine(l)
}

// isATXHeading: 1-6 `#` then a space (or EOL). No leading indent
// allowed past 3 spaces (CommonMark rule); we keep the line verbatim
// either way and just answer "is this a heading?".
func isATXHeading(l string) bool {
	t := trimLeadingSpaces(l, 3)
	if len(t) == 0 || t[0] != '#' {
		return false
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	if n < 1 || n > 6 {
		return false
	}
	if n == len(t) {
		return true
	}
	return t[n] == ' ' || t[n] == '\t'
}

// isSetextUnderline matches a `=+` or `-+` line (CommonMark allows
// up to 3 leading spaces and trailing spaces). Strict variant: at
// least one marker character.
func isSetextUnderline(l string) bool {
	t := strings.TrimRight(trimLeadingSpaces(l, 3), " \t")
	if len(t) == 0 {
		return false
	}
	c := t[0]
	if c != '=' && c != '-' {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] != c {
			return false
		}
	}
	return true
}

// isFenceOpen matches ```... or ~~~... (3 or more) at start of line
// (up to 3 leading spaces).
func isFenceOpen(l string) bool {
	_, ok := fenceOpener(l)
	return ok
}

// fenceOpener returns the fence string ("```" or "~~~") and ok=true
// if l opens a fenced code block. The closing fence must use the
// same character and be at least as long as the opener.
func fenceOpener(l string) (string, bool) {
	t := trimLeadingSpaces(l, 3)
	if len(t) < 3 {
		return "", false
	}
	c := t[0]
	if c != '`' && c != '~' {
		return "", false
	}
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n < 3 {
		return "", false
	}
	return strings.Repeat(string(c), n), true
}

// consumeFence returns the verbatim block (opener + body + closer if
// present) and the index of the first line after it. Unterminated
// fences run to EOF; we still return the block intact, minus any
// trailing blank lines — those belong to the document (as empty
// paragraphs), not to a code block whose end we had to guess.
func consumeFence(lines []string, start int) (string, int) {
	opener, _ := fenceOpener(lines[start])
	c := opener[0]
	end := len(lines)
	terminated := false
	for j := start + 1; j < len(lines); j++ {
		t := strings.TrimRight(trimLeadingSpaces(lines[j], 3), " \t")
		if len(t) >= len(opener) && allByte(t, c) {
			end = j + 1
			terminated = true
			break
		}
	}
	if !terminated {
		for end > start+1 && isBlank(lines[end-1]) {
			end--
		}
	}
	return strings.Join(lines[start:end], "\n"), end
}

// isBlockquoteLine: leading `>` after up to 3 spaces. CommonMark
// allows lazy continuations but we keep the strict form here — the
// run ends at the first non-`>` non-blank line.
func isBlockquoteLine(l string) bool {
	t := trimLeadingSpaces(l, 3)
	return len(t) > 0 && t[0] == '>'
}

func consumeBlockquote(lines []string, start int) (string, int) {
	end := start + 1
	for end < len(lines) && isBlockquoteLine(lines[end]) {
		end++
	}
	return strings.Join(lines[start:end], "\n"), end
}

// isListItemLine matches an unordered (`-`, `*`, `+`) or ordered
// (`\d+.` / `\d+)`) item at column 0..3.
func isListItemLine(l string) bool {
	_, ok := listItemMarker(l)
	return ok
}

// listItemMarker reports the indent at which the item's continuation
// would be expected (the column right after the marker + space). The
// caller uses it to decide what counts as a continuation line.
func listItemMarker(l string) (contIndent int, ok bool) {
	leading := 0
	for leading < len(l) && leading < 4 && (l[leading] == ' ' || l[leading] == '\t') {
		leading++
	}
	if leading >= len(l) {
		return 0, false
	}
	c := l[leading]
	switch c {
	case '-', '*', '+':
		if leading+1 == len(l) {
			return leading + 2, true
		}
		if l[leading+1] == ' ' || l[leading+1] == '\t' {
			return leading + 2, true
		}
		return 0, false
	}
	if !isDigit(c) {
		return 0, false
	}
	j := leading
	for j < len(l) && isDigit(l[j]) && j-leading < 9 {
		j++
	}
	if j == leading {
		return 0, false
	}
	if j >= len(l) {
		return 0, false
	}
	if l[j] != '.' && l[j] != ')' {
		return 0, false
	}
	if j+1 == len(l) {
		return j + 2, true
	}
	if l[j+1] == ' ' || l[j+1] == '\t' {
		return j + 2, true
	}
	return 0, false
}

// consumeListItem takes one item — its first line plus any
// continuation lines (indented at >= contIndent, or blank lines
// followed by another indented continuation). Stops at the next
// list-item line at the same-or-shallower indent, at a blank-then-
// non-indented line, or at EOF.
func consumeListItem(lines []string, start int) (string, int) {
	cont, _ := listItemMarker(lines[start])
	end := start + 1
	for end < len(lines) {
		l := lines[end]
		if isBlank(l) {
			if end+1 < len(lines) && indentColumns(lines[end+1]) >= cont && !isBlank(lines[end+1]) {
				end++
				continue
			}
			break
		}
		if _, ok := listItemMarker(l); ok && indentColumns(l) < cont {
			break
		}
		if indentColumns(l) >= cont {
			end++
			continue
		}
		break
	}
	for end > start+1 && isBlank(lines[end-1]) {
		end--
	}
	return strings.Join(lines[start:end], "\n"), end
}

// isHTMLBlockLine: very loose — line starts with `<` after up to 3
// spaces. Good enough for v1 since HTML blocks are stored opaquely.
func isHTMLBlockLine(l string) bool {
	t := trimLeadingSpaces(l, 3)
	if len(t) < 2 || t[0] != '<' {
		return false
	}
	c := t[1]
	return c == '/' || c == '!' || c == '?' || isASCIILetter(c)
}

// isTableLine: starts with `|`, after up to 3 spaces.
func isTableLine(l string) bool {
	t := trimLeadingSpaces(l, 3)
	return len(t) > 0 && t[0] == '|'
}

// consumeUntilBlank reads consecutive non-blank lines as one block.
func consumeUntilBlank(lines []string, start int) (string, int) {
	end := start + 1
	for end < len(lines) && !isBlank(lines[end]) {
		end++
	}
	return strings.Join(lines[start:end], "\n"), end
}

// consumeParagraph reads a paragraph: from start until a blank line,
// or until a different block opens on a subsequent line.
func consumeParagraph(lines []string, start int) (string, int) {
	end := start + 1
	for end < len(lines) {
		l := lines[end]
		if isBlank(l) {
			break
		}
		if isATXHeading(l) || isFenceOpen(l) || isBlockquoteLine(l) ||
			isListItemLine(l) || isHTMLBlockLine(l) || isTableLine(l) {
			break
		}
		end++
	}
	return strings.Join(lines[start:end], "\n"), end
}

func trimLeadingSpaces(l string, max int) string {
	n := 0
	for n < len(l) && n < max && l[n] == ' ' {
		n++
	}
	return l[n:]
}

// indentColumns counts leading whitespace columns, treating a tab as
// advancing to the next multiple of 4. Good enough for v1.
func indentColumns(l string) int {
	col := 0
	for i := 0; i < len(l); i++ {
		switch l[i] {
		case ' ':
			col++
		case '\t':
			col += 4 - col%4
		default:
			return col
		}
	}
	return col
}

func isDigit(b byte) bool       { return b >= '0' && b <= '9' }
func isASCIILetter(b byte) bool { return unicode.IsLetter(rune(b)) && b < 0x80 }

func allByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != c {
			return false
		}
	}
	return true
}
