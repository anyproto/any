package markdown

import (
	"strings"
	"unicode"
)

// Split breaks raw markdown into block-records.
//
// A "block" is one of:
//
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
//
// Blank lines are dropped entirely — they are pure separators between
// blocks. The returned slice contains no empty entries.
//
// The returned strings carry no trailing newline. Joining with
// "\n\n" reconstructs a canonical markdown document; round-trip is
// idempotent — Split(Join(Split(x))) == Split(x).
func Split(md string) []string {
	if md == "" {
		return nil
	}
	lines := splitLines(md)
	var out []string
	i := 0
	for i < len(lines) {
		if isBlank(lines[i]) {
			i++
			continue
		}

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
// fences run to EOF; we still return the block intact.
func consumeFence(lines []string, start int) (string, int) {
	opener, _ := fenceOpener(lines[start])
	c := opener[0]
	end := len(lines)
	for j := start + 1; j < len(lines); j++ {
		t := strings.TrimRight(trimLeadingSpaces(lines[j], 3), " \t")
		if len(t) >= len(opener) && allByte(t, c) {
			end = j + 1
			break
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
