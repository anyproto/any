package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yuin/goldmark"
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

func TestRunDownloadsFromRenumberedQuickstarts(t *testing.T) {
	for _, directory := range []string{"02-quickstart", "19-quickstart"} {
		t.Run(directory, func(t *testing.T) {
			src, out := t.TempDir(), filepath.Join(t.TempDir(), "site")
			writeDocFixture(t, src, "index.md", "# Home\n")
			writeDocFixture(t, src, directory+"/javascript.md", strings.Join([]string{
				"---", "title: JavaScript", "---", "# Client", "",
				"[Download](../assets/examples/client.mjs)", "",
				"```sh", "notJavaScript", "```", "",
				"````text", "```js", "illustrativeOnly();", "```", "````", "",
				"````js", "const value = 1;", "/*", "```", "*/", "````", "",
				"~~~js title=next", "console.log(value);", "~~~", "",
			}, "\n"))
			writeDocFixture(t, src, directory+"/python.md", strings.Join([]string{
				"# Client", "", "```python", "value = 1", "```", "",
				"```json", "{}", "```", "", "```python", "print(value)", "```",
			}, "\r\n"))
			if err := run(src, out); err != nil {
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
		})
	}
}

func TestRunWithoutQuickstarts(t *testing.T) {
	src, out := t.TempDir(), filepath.Join(t.TempDir(), "site")
	writeDocFixture(t, src, "index.md", "# Home\n")
	if err := run(src, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "assets", "examples")); !os.IsNotExist(err) {
		t.Fatalf("examples directory error = %v, want no generated downloads", err)
	}
	// A custom source can also contain only one of the two clients.
	writeDocFixture(t, src, "12-quickstart/python.md", "```python\nprint('hello')\n```\n")
	if err := run(src, out); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(out, "assets", "examples", "client.py"))
	if err != nil || string(got) != "print('hello')\n\n" {
		t.Fatalf("Python download = %q, error = %v", got, err)
	}
}

func TestRunRejectsDownloadsWithoutSource(t *testing.T) {
	for _, tt := range []struct{ name, source, body, missing string }{
		{"relative", "index.md", "[Download](assets/examples/client.mjs)", "/quickstart/javascript.html"},
		{"nested", "08-guide/client.md", "<a href='../assets/examples/client.py' download>Download</a>", "/quickstart/python.html"},
		{"absolute", "index.md", "[Download](/assets/examples/client.py?download=1)", "/quickstart/python.html"},
		{"encoded", "index.md", "[Download](assets/examples/client%2Emjs)", "/quickstart/javascript.html"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src := t.TempDir()
			writeDocFixture(t, src, "index.md", "# Home\n")
			writeDocFixture(t, src, tt.source, tt.body)
			err := run(src, filepath.Join(t.TempDir(), "site"))
			if err == nil || !strings.Contains(err.Error(), tt.source+": download ") || !strings.Contains(err.Error(), tt.missing) {
				t.Fatalf("error = %v, want referrer and missing source %s", err, tt.missing)
			}
		})
	}
}

func TestRunAllowsExternalDownloadLinks(t *testing.T) {
	src := t.TempDir()
	writeDocFixture(t, src, "index.md", "[Download](https://example.com/assets/examples/client.mjs)\n\n[Python](//example.com/assets/examples/client.py)")
	if err := run(src, filepath.Join(t.TempDir(), "site")); err != nil {
		t.Fatal(err)
	}
}

func TestWriteExamplesErrors(t *testing.T) {
	client := &page{Src: "19-quickstart/javascript.md", URL: "/quickstart/javascript.html"}
	t.Run("missing language", func(t *testing.T) {
		src := t.TempDir()
		writeDocFixture(t, src, client.Src, "```sh\necho hello\n```\n")
		err := writeExamples(src, t.TempDir(), []*page{client}, goldmark.New())
		if err == nil || !strings.Contains(err.Error(), "no js examples found in "+client.Src) {
			t.Fatalf("error = %v, want missing language and source page", err)
		}
	})
	t.Run("missing source", func(t *testing.T) {
		if err := writeExamples(t.TempDir(), t.TempDir(), []*page{client}, goldmark.New()); err == nil {
			t.Fatal("missing source was swallowed")
		}
	})
	t.Run("unreadable source", func(t *testing.T) {
		src := t.TempDir()
		if err := os.MkdirAll(filepath.Join(src, client.Src), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeExamples(src, t.TempDir(), []*page{client}, goldmark.New()); err == nil {
			t.Fatal("source read error was swallowed")
		}
	})
	t.Run("output blocked by file", func(t *testing.T) {
		src, out := t.TempDir(), t.TempDir()
		writeDocFixture(t, src, client.Src, "```js\nconsole.log('hello');\n```\n")
		writeDocFixture(t, out, "assets", "not a directory")
		if err := writeExamples(src, out, []*page{client}, goldmark.New()); err == nil {
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

func TestSearchPartsSharesBudgetWithoutDroppingSections(t *testing.T) {
	parts := []part{
		{Text: strings.Repeat("opening ", searchTextLimit)},
		{Heading: "Short", ID: "short", Text: "Small section."},
		{Heading: "Later", ID: "later", Text: strings.Repeat("記憶🧠", searchTextLimit)},
	}
	got := searchParts(parts)
	if len(got) != len(parts) {
		t.Fatalf("search has %d sections, want %d", len(got), len(parts))
	}
	total := 0
	for i, p := range got {
		if p.Heading != parts[i].Heading || p.ID != parts[i].ID || p.Text == "" {
			t.Errorf("section %d lost its heading, target or text: %+v", i, p)
		}
		if !utf8.ValidString(p.Text) || !strings.HasPrefix(parts[i].Text, p.Text) {
			t.Errorf("section %d has invalid or changed text", i)
		}
		total += utf8.RuneCountInString(p.Text)
	}
	if total != searchTextLimit {
		t.Errorf("indexed text has %d characters, want %d", total, searchTextLimit)
	}
	if got[1].Text != parts[1].Text {
		t.Error("short section was unnecessarily truncated")
	}
	if utf8.RuneCountInString(got[0].Text) != utf8.RuneCountInString(got[2].Text) {
		t.Error("long sections did not share the remaining budget equally")
	}
	if len(parts[0].Text) != len("opening ")*searchTextLimit {
		t.Error("search truncation changed the original page parts")
	}
}

func TestSearchPartsKeepsShortPages(t *testing.T) {
	for _, parts := range [][]part{nil, {{Text: "Hello"}, {Heading: "Empty", ID: "empty"}}} {
		if got := searchParts(parts); !reflect.DeepEqual(got, parts) {
			t.Fatalf("searchParts = %+v, want unchanged %+v", got, parts)
		}
	}
}

func TestTruncatePreservesUnicode(t *testing.T) {
	for _, tt := range []struct {
		input string
		limit int
		want  string
	}{
		{"", 0, ""},
		{"hello", 0, ""},
		{"hello", 5, "hello"},
		{"short", 10, "short"},
		{"Aé界🧠Z", 4, "Aé界🧠"},
		{"🧠memory", 1, "🧠"},
	} {
		if got := truncate(tt.input, tt.limit); got != tt.want || !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.limit, got, tt.want)
		}
	}
}

func TestRunBoundsSearchTextAndRetainsLateTargets(t *testing.T) {
	src, out := t.TempDir(), filepath.Join(t.TempDir(), "site")
	writeDocFixture(t, src, "index.md", "# Home\n\n"+strings.Repeat("前", searchTextLimit*2)+
		"\n\n## Middle\n\nBrief explanation.\n\n## Last\n\n"+strings.Repeat("🧠", searchTextLimit*2)+" tail-sentinel\n")
	if err := run(src, out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "search.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index []struct{ Parts []part }
	if err := json.Unmarshal(raw, &index); err != nil {
		t.Fatal(err)
	}
	if len(index) != 1 || len(index[0].Parts) != 3 {
		t.Fatalf("unexpected search page/section count: %+v", index)
	}
	total := 0
	for _, p := range index[0].Parts {
		total += utf8.RuneCountInString(p.Text)
	}
	if total > searchTextLimit {
		t.Fatalf("search page contains %d characters, limit %d", total, searchTextLimit)
	}
	last := index[0].Parts[2]
	if last.Heading != "Last" || last.ID != "last" || !strings.HasPrefix(last.Text, "🧠") {
		t.Fatalf("late section is not searchable: %+v", last)
	}
	page, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil || !strings.Contains(string(page), "tail-sentinel") {
		t.Fatalf("rendered page lost its full text: %v", err)
	}
}
