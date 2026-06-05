package main

import "testing"

func TestSplitToolMarkdown(t *testing.T) {
	md := `## Tool Description

Client for things. Second line.

## Tool Schema

### setup [setup]

Pre-bound as a kernel global.

### createType(opts) [mutator]

**Input:**
- opts.key (string)

**Output:** ` + "`{ok}`" + `

### ask(prompt)

One-shot completion.

### How to craft good queries

Prose guidance, no signature.
`
	desc, methods := splitToolMarkdown(md)

	if want := "Client for things. Second line."; desc != want {
		t.Errorf("description = %q, want %q", desc, want)
	}
	if len(methods) != 4 {
		t.Fatalf("got %d methods, want 4: %+v", len(methods), methods)
	}

	checks := []struct {
		bare, name, kind string
	}{
		{"setup", "setup", "setup"},
		{"createType", "createType(opts)", "mutator"},
		{"ask", "ask(prompt)", "getter"}, // untagged → getter
		{"How to craft good queries", "How to craft good queries", "getter"},
	}
	for i, c := range checks {
		m := methods[i]
		if m.BareName != c.bare || m.Name != c.name || m.Kind != c.kind || m.Pos != i {
			t.Errorf("method %d = {bare:%q name:%q kind:%q pos:%d}, want {%q %q %q %d}",
				i, m.BareName, m.Name, m.Kind, m.Pos, c.bare, c.name, c.kind, i)
		}
	}

	// Body is byte-exact modulo surrounding blank lines; heading excluded.
	if want := "**Input:**\n- opts.key (string)\n\n**Output:** `{ok}`"; methods[1].Text != want {
		t.Errorf("createType text = %q, want %q", methods[1].Text, want)
	}
}

func TestSplitToolMarkdownNoSchema(t *testing.T) {
	desc, methods := splitToolMarkdown("## Tool Description\n\nJust a description.\n")
	if desc != "Just a description." {
		t.Errorf("description = %q", desc)
	}
	if len(methods) != 0 {
		t.Errorf("got %d methods, want 0", len(methods))
	}
}

func TestSplitToolMarkdownEmpty(t *testing.T) {
	desc, methods := splitToolMarkdown("")
	if desc != "" || len(methods) != 0 {
		t.Errorf("empty md: desc=%q methods=%d", desc, len(methods))
	}
}

func TestSplitToolMarkdownSchemaEndsAtSibling(t *testing.T) {
	md := "## Tool Description\n\nD.\n\n## Tool Schema\n\n### a()\n\nbody a\n\n## Notes\n\n### not a method\n"
	_, methods := splitToolMarkdown(md)
	if len(methods) != 1 || methods[0].BareName != "a" {
		t.Fatalf("methods = %+v, want just a()", methods)
	}
	if methods[0].Text != "body a" {
		t.Errorf("a text = %q", methods[0].Text)
	}
}

func TestSplitToolMarkdownDuplicateBareName(t *testing.T) {
	md := "## Tool Schema\n\n### run(a)\n\nfirst\n\n### run(b)\n\nsecond\n"
	_, methods := splitToolMarkdown(md)
	if len(methods) != 2 {
		t.Fatalf("got %d methods", len(methods))
	}
	if methods[0].BareName != "run" || methods[1].BareName != "run-1" {
		t.Errorf("bare names = %q, %q — want run, run-1", methods[0].BareName, methods[1].BareName)
	}
}

func TestSplitToolMarkdownToolsHeadingVariant(t *testing.T) {
	// "# Tools" at level 1 → methods are "## name" subsections.
	md := "## Tool Description\n\nD.\n\n# Tools\n\n## doIt(x) [mutator]\n\nbody\n"
	_, methods := splitToolMarkdown(md)
	if len(methods) != 1 || methods[0].Name != "doIt(x)" || methods[0].Kind != "mutator" {
		t.Fatalf("methods = %+v", methods)
	}
}
