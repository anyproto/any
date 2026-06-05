package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Tool markdown is authored as one file (tool-descriptions/<name>.md) but
// STORED split: the "## Tool Description" section body goes to the
// program_description dataset (record "main", field "text"), and each
// "### name(sig) [kind]" subsection under "## Tool Schema" becomes one
// program_methods record (id = bareName, fields name/kind/text/pos). The
// split is the storage contract — readers (anyHelper.getToolDocs,
// toolcall_core's prompt builder, any-ui) consume the datasets and never
// re-parse the full markdown. This parser mirrors the heading walk the JS
// side historically did in toolcall_core's _parseToolMarkdown, so existing
// .md files split without rewrites.

// methodDoc is one "### heading" subsection of the schema section.
type methodDoc struct {
	BareName string // record id: heading text up to the first "(" (or the full heading when no signature)
	Name     string // heading text with signature, WITHOUT the kind tag: "createType(opts)"
	Kind     string // getter | mutator | setup | program (default getter)
	Text     string // body lines after the heading (heading itself excluded)
	Pos      int    // order index, the read-back sort key
}

var (
	headingRe = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	kindTagRe = regexp.MustCompile(`\s*\[(getter|mutator|setup|program)\]\s*$`)
)

// splitToolMarkdown splits a full tool markdown into the description section
// body and the per-method docs. Mirrors the historical JS parse: description
// = the "## Tool Description" section (trimmed); methods = every subsection
// one level below the "## Tool Schema" (or "# Tools" / "## Tools") heading.
// Prose subsections without a signature are methods too (kind defaults to
// getter unless tagged, e.g. "### Worked example [setup]").
func splitToolMarkdown(md string) (string, []methodDoc) {
	description := extractSection(md, "Tool Description")

	toolsStart := -1
	for _, cand := range []string{"\n# Tools\n", "# Tools\n", "\n## Tools\n", "## Tools\n", "\n## Tool Schema\n", "## Tool Schema\n"} {
		if idx := strings.Index(md, cand); idx != -1 {
			toolsStart = idx
			if cand[0] == '\n' {
				toolsStart++
			}
			break
		}
	}
	if toolsStart == -1 {
		return description, nil
	}

	lines := strings.Split(md[toolsStart:], "\n")
	sectionLevel := 1
	if m := headingRe.FindStringSubmatch(lines[0]); m != nil {
		sectionLevel = len(m[1])
	}
	methodPrefix := strings.Repeat("#", sectionLevel+1) + " "

	var methods []methodDoc
	seen := map[string]bool{}
	var cur *methodDoc
	var curLines []string

	flush := func() {
		if cur == nil {
			return
		}
		// Body is stored without the surrounding blank lines; renderers
		// reconstruct the section as heading + "\n\n" + text.
		cur.Text = strings.Trim(strings.Join(curLines, "\n"), "\n")
		if seen[cur.BareName] {
			// Record id must be unique; duplicate headings get a -<pos> suffix.
			cur.BareName = fmt.Sprintf("%s-%d", cur.BareName, cur.Pos)
		}
		seen[cur.BareName] = true
		methods = append(methods, *cur)
		cur, curLines = nil, nil
	}

	for i := 1; i < len(lines); i++ {
		if m := headingRe.FindStringSubmatch(lines[i]); m != nil && len(m[1]) <= sectionLevel {
			break // next sibling/parent section ends the schema walk
		}
		if strings.HasPrefix(lines[i], methodPrefix) {
			flush()
			heading := strings.TrimSpace(lines[i][len(methodPrefix):])
			kind := "getter"
			if km := kindTagRe.FindStringSubmatchIndex(heading); km != nil {
				kind = heading[km[2]:km[3]]
				heading = strings.TrimSpace(heading[:km[0]])
			}
			cur = &methodDoc{
				BareName: methodBareName(heading),
				Name:     heading,
				Kind:     kind,
				Pos:      len(methods),
			}
			curLines = nil
		} else if cur != nil {
			curLines = append(curLines, lines[i])
		}
	}
	flush()
	return description, methods
}

// methodBareName is the callable name: heading text up to the first "(".
// A prose heading without a signature keeps its full text.
func methodBareName(name string) string {
	if idx := strings.Index(name, "("); idx > 0 {
		return strings.TrimSpace(name[:idx])
	}
	return strings.TrimSpace(name)
}

// extractSection returns the trimmed body of the markdown section whose
// heading title equals sectionName (case-insensitive), at any heading level,
// ending at the next heading of the same or higher level. "" when absent.
func extractSection(md, sectionName string) string {
	var captured []string
	capturing := false
	captureLevel := 0
	for line := range strings.SplitSeq(md, "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			title := strings.TrimSpace(m[2])
			if capturing && level <= captureLevel {
				break
			}
			if strings.EqualFold(title, sectionName) {
				capturing = true
				captureLevel = level
				continue
			}
		}
		if capturing {
			captured = append(captured, line)
		}
	}
	return strings.TrimSpace(strings.Join(captured, "\n"))
}
