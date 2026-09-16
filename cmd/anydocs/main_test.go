package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSplitFront(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want front
		body string
	}{
		{name: "plain markdown", raw: "# Guide\n\n---\n", body: "# Guide\n\n---\n"},
		{name: "bare rule", raw: "---", body: "---"},
		{
			name: "metadata and unchanged body",
			raw:  "---\ntitle: Guide\ndescription: 'A: description'\norder: 12\n---\n# Body\n\n---\n",
			want: front{Title: "Guide", Description: "A: description", Order: 12},
			body: "# Body\n\n---\n",
		},
		{
			name: "CRLF",
			raw:  "---\r\ntitle: Guide\r\norder: 2\r\n---\r\n# Body\r\n",
			want: front{Title: "Guide", Order: 2},
			body: "# Body\r\n",
		},
		{name: "empty metadata", raw: "---\n---\n# Body\n", body: "# Body\n"},
		{name: "closing delimiter at EOF", raw: "---\ntitle: Guide\n---", want: front{Title: "Guide"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := splitFront([]byte(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if fm != tt.want || string(body) != tt.body {
				t.Fatalf("splitFront = (%+v, %q), want (%+v, %q)", fm, body, tt.want, tt.body)
			}
		})
	}
}

func TestSplitFrontRejectsInvalidMetadata(t *testing.T) {
	for _, tt := range []struct{ name, raw, message string }{
		{"missing close", "---\ntitle: Guide\n", "missing its closing delimiter"},
		{"malformed YAML", "---\ntitle: [unterminated\n---\n# Body", "invalid front matter"},
		{"malformed CRLF YAML", "---\r\ntitle: [unterminated\r\n---\r\n# Body", "invalid front matter"},
		{"wrong order type", "---\norder: later\n---\n# Body", "invalid front matter"},
		{"duplicate key", "---\ntitle: First\ntitle: Second\n---\n# Body", "invalid front matter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, body, err := splitFront([]byte(tt.raw))
			if err == nil || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("error = %v, want %q", err, tt.message)
			}
			if body != nil {
				t.Fatalf("invalid metadata returned renderable body %q", body)
			}
		})
	}
}

func TestRunReportsFrontMatterSource(t *testing.T) {
	src := t.TempDir()
	writeDocFixture(t, src, "01-start/index.md", "---\norder: later\n---\n# Start\n")
	err := run(src, filepath.Join(t.TempDir(), "site"))
	if err == nil || !strings.Contains(filepath.ToSlash(err.Error()), "01-start/index.md: invalid front matter") {
		t.Fatalf("run error = %v, want source path and metadata error", err)
	}
}

func TestWriteExamplesUsesMarkdownFences(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	writeDocFixture(t, src, "02-quickstart/javascript.md", strings.Join([]string{
		"---", "title: JavaScript", "---", "# Client", "",
		"```sh", "notJavaScript", "```", "",
		"````text", "```js", "illustrativeOnly();", "```", "````", "",
		"````js", "const value = 1;", "/*", "```", "*/", "````", "",
		"~~~js title=next", "console.log(value);", "~~~", "",
	}, "\n"))
	writeDocFixture(t, src, "02-quickstart/python.md", strings.Join([]string{
		"# Client", "", "```python", "value = 1", "```", "",
		"```json", "{}", "```", "", "```python", "print(value)", "```",
	}, "\r\n"))
	if err := writeExamples(src, out); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"client.mjs": "const value = 1;\n/*\n```\n*/\n\nconsole.log(value);\n\n",
		"client.py":  "value = 1\r\n\nprint(value)\r\n\n",
	} {
		got, err := os.ReadFile(filepath.Join(out, "assets", "examples", name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestWriteExamplesWithoutQuickstarts(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	if err := writeExamples(src, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "assets", "examples")); !os.IsNotExist(err) {
		t.Fatalf("examples directory error = %v, want no generated downloads", err)
	}
	// A custom source can also contain only one of the two clients.
	writeDocFixture(t, src, "02-quickstart/python.md", "```python\nprint('hello')\n```\n")
	if err := writeExamples(src, out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, "assets", "examples", "client.py"))
	if err != nil || string(got) != "print('hello')\n\n" {
		t.Fatalf("Python download = %q, error = %v", got, err)
	}
}

func TestWriteExamplesErrors(t *testing.T) {
	t.Run("missing language", func(t *testing.T) {
		src := t.TempDir()
		writeDocFixture(t, src, "02-quickstart/javascript.md", "```sh\necho hello\n```\n")
		err := writeExamples(src, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "no js examples found in javascript.md") {
			t.Fatalf("error = %v, want missing language and source page", err)
		}
	})
	t.Run("unreadable source", func(t *testing.T) {
		src := t.TempDir()
		if err := os.MkdirAll(filepath.Join(src, "02-quickstart", "javascript.md"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeExamples(src, t.TempDir()); err == nil {
			t.Fatal("source read error was swallowed")
		}
	})
	t.Run("output blocked by file", func(t *testing.T) {
		src, out := t.TempDir(), t.TempDir()
		writeDocFixture(t, src, "02-quickstart/javascript.md", "```js\nconsole.log('hello');\n```\n")
		writeDocFixture(t, out, "assets", "not a directory")
		if err := writeExamples(src, out); err == nil {
			t.Fatal("output write error was swallowed")
		}
	})
}

func writeDocFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlain(t *testing.T) {
	got := plain(`<p>400 (<code>chat.text_too_long</code>) &amp; <strong>"x"</strong></p><td>a</td><td>b</td>`)
	if want := `400 (chat.text_too_long) & "x" a b`; got != want {
		t.Fatalf("plain = %q, want %q", got, want)
	}
}

func TestSplit(t *testing.T) {
	h := `<h1>Page</h1><p>lead</p>` +
		`<h2 id="one">One <code>x</code></h2><p>first</p>` +
		`<h3 id="two">Two</h3>` +
		`<h2 id="three">Three</h2><p>` + strings.Repeat("long ", 2000) + `tail</p>`
	got := split(h)
	want := []part{
		{Text: "Page lead"},
		{Heading: "One x", ID: "one", Text: "first"},
		{Heading: "Two", ID: "two", Text: ""},
		{Heading: "Three", ID: "three", Text: strings.Repeat("long ", 2000) + "tail"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("split = %+v", got)
	}
}
