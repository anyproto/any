package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRewriteDocTarget(t *testing.T) {
	tests := map[string]string{
		"guide.html":                                  "guide.md",
		"../database/objects.html#shape":              "../database/objects.md#shape",
		"/reference/http-api.html?view=full#endpoint": "/reference/http-api.md?view=full#endpoint",
		"https://docs.any.org/guide.html":             "https://docs.any.org/guide.md",
		"//docs.any.org/guide.html":                   "//docs.any.org/guide.md",
		"https://example.com/guide.html":              "https://example.com/guide.html",
		"mailto:docs@example.com":                     "mailto:docs@example.com",
		"guide.md":                                    "guide.md",
		"image.html.png":                              "image.html.png",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := rewriteDocTarget(input); got != want {
				t.Fatalf("rewriteDocTarget(%q) = %q, want %q", input, got, want)
			}
		})
	}
}

func TestRewriteMarkdownLinks(t *testing.T) {
	input := []byte(`# Links

[relative](../database/objects.html#shape)
[root](/reference/http-api.html?view=full#endpoint)
[same origin](https://docs.any.org/understanding/local-first.html)
[external](https://example.com/reference.html)
[with title](guide.html "Guide")
[angle](<guide.html>)
[reference]: guide.html "Guide"
<a href="guide.html#raw">Double</a>
<a href='guide.html?raw=1'>Single</a>
<https://docs.any.org/guide.html>
<https://example.com/guide.html>

- Nested list
    [nested](guide.html)

After the list.

    [indented code](indented.html)
    <https://docs.any.org/indented.html>

` + "`[inline code](inline.html)`" + `

` + "```markdown" + `
[fenced](fenced.html)
<a href="fenced.html">Fenced</a>
` + "```" + `

~~~html
<a href="tilde.html">Tilde</a>
~~~
`)

	want := []byte(`# Links

[relative](../database/objects.md#shape)
[root](/reference/http-api.md?view=full#endpoint)
[same origin](https://docs.any.org/understanding/local-first.md)
[external](https://example.com/reference.html)
[with title](guide.md "Guide")
[angle](<guide.md>)
[reference]: guide.md "Guide"
<a href="guide.md#raw">Double</a>
<a href='guide.md?raw=1'>Single</a>
<https://docs.any.org/guide.md>
<https://example.com/guide.html>

- Nested list
    [nested](guide.md)

After the list.

    [indented code](indented.html)
    <https://docs.any.org/indented.html>

` + "`[inline code](inline.html)`" + `

` + "```markdown" + `
[fenced](fenced.html)
<a href="fenced.html">Fenced</a>
` + "```" + `

~~~html
<a href="tilde.html">Tilde</a>
~~~
`)

	if got := rewriteMarkdownLinks(input); !bytes.Equal(got, want) {
		t.Fatalf("rewritten markdown:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunGeneratesHTMLAndMarkdownTwins(t *testing.T) {
	src := filepath.Join(t.TempDir(), "website")
	out := filepath.Join(t.TempDir(), "dist")
	writeTestFile(t, filepath.Join(src, "index.md"), `---
title: Home
description: Start here.
---
# Home

<a href="guide/index.html"><strong>Guide</strong></a>
`)
	writeTestFile(t, filepath.Join(src, "01-guide", "index.md"), `---
title: Guide
description: Learn the product.
order: 1
---
# Guide

Continue with [Topic](topic.html).
`)
	writeTestFile(t, filepath.Join(src, "01-guide", "topic.md"), `---
title: Topic
description: A focused topic.
order: 10
---
# Topic

Return [home](../index.html).
`)

	if err := run(src, out); err != nil {
		t.Fatal(err)
	}

	markdown := readTestFile(t, filepath.Join(out, "guide", "topic.md"))
	metadata := generatedMetadata(t, markdown)
	wantMetadata := map[string]string{
		"title":               "Topic",
		"description":         "A focused topic.",
		"canonical_url":       "https://docs.any.org/guide/topic.html",
		"documentation_index": "https://docs.any.org/llms.txt",
	}
	if len(metadata) != len(wantMetadata) {
		t.Fatalf("generated metadata has keys %v, want exactly %v", metadata, wantMetadata)
	}
	for key, want := range wantMetadata {
		if got := metadata[key]; got != want {
			t.Errorf("metadata[%q] = %q, want %q", key, got, want)
		}
	}
	if !bytes.Contains(markdown, []byte("Return [home](../index.md).")) {
		t.Errorf("generated markdown did not rewrite internal link:\n%s", markdown)
	}

	indexMarkdown := readTestFile(t, filepath.Join(out, "index.md"))
	if !bytes.Contains(indexMarkdown, []byte(`href="guide/index.md"`)) {
		t.Errorf("generated home markdown did not rewrite raw HTML link:\n%s", indexMarkdown)
	}

	html := string(readTestFile(t, filepath.Join(out, "guide", "topic.html")))
	for _, want := range []string{
		`href="../guide/topic.md"`,
		`href="../llms.txt"`,
		`href="../index.html"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("generated HTML missing %q", want)
		}
	}

	llms := string(readTestFile(t, filepath.Join(out, "llms.txt")))
	for _, want := range []string{
		"## Start",
		"[Home](https://docs.any.org/index.md)",
		"## Guide",
		"[Overview](https://docs.any.org/guide/index.md)",
		"[Topic](https://docs.any.org/guide/topic.md)",
	} {
		if !strings.Contains(llms, want) {
			t.Errorf("llms.txt missing %q:\n%s", want, llms)
		}
	}
	if strings.Contains(llms, ".html)") {
		t.Errorf("llms.txt contains an HTML page link:\n%s", llms)
	}

	search := string(readTestFile(t, filepath.Join(out, "search.json")))
	if !strings.Contains(search, `"URL":"/guide/topic.html"`) {
		t.Errorf("search index no longer points to HTML: %s", search)
	}
	if strings.Contains(search, "/guide/topic.md") {
		t.Errorf("search index unexpectedly points to markdown: %s", search)
	}

	if _, err := os.Stat(filepath.Join(out, "guide", "topic.html.md")); !os.IsNotExist(err) {
		t.Fatalf("unexpected .html.md output: %v", err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func generatedMetadata(t *testing.T, markdown []byte) map[string]string {
	t.Helper()
	if !bytes.HasPrefix(markdown, []byte("---\n")) {
		t.Fatalf("generated markdown has no frontmatter:\n%s", markdown)
	}
	rest := markdown[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		t.Fatalf("generated markdown has unterminated frontmatter:\n%s", markdown)
	}
	var metadata map[string]string
	if err := yaml.Unmarshal(rest[:end], &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}
