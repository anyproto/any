// Command anydocs renders the website/ markdown tree into a static HTML
// site. No config: folder order comes from the NN- filename prefix, page
// order from `order:` front-matter (then filename), titles from `title:`.
//
//	anydocs [-src website] [-out website/dist]
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"
)

type front struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Order       int    `yaml:"order"`
}

type page struct {
	front
	Section *section
	Src     string // relative source path
	URL     string // absolute site path, e.g. /database/objects.html
	Body    template.HTML
	Text    string // plain-ish text for the search index
	Prev    *page
	Next    *page
	IsIndex bool
}

type section struct {
	Dir   string // e.g. 03-database
	Slug  string // e.g. database
	Title string
	Order int
	Pages []*page
	Index *page
}

var (
	prefixRe = regexp.MustCompile(`^(\d+)-(.*)$`)
	tagRe    = regexp.MustCompile(`<[^>]*>`)
	wsRe     = regexp.MustCompile(`\s+`)
)

func main() {
	src := flag.String("src", "website", "source dir")
	out := flag.String("out", "website/dist", "output dir")
	flag.Parse()
	if err := run(*src, *out); err != nil {
		fmt.Fprintln(os.Stderr, "anydocs:", err)
		os.Exit(1)
	}
}

func run(src, out string) error {
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.Footnote),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	tpl := template.Must(template.New("page").Funcs(template.FuncMap{"hasPrefix": strings.HasPrefix}).Parse(pageTpl))

	var sections []*section
	byDir := map[string]*section{}
	var home *page

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if rel == "dist" || rel == "assets" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_") {
				return filepath.SkipDir
			}
			if strings.Contains(rel, string(filepath.Separator)) {
				return nil // one level only
			}
			s := &section{Dir: rel, Slug: rel, Title: humanize(rel), Order: 1 << 20}
			if m := prefixRe.FindStringSubmatch(rel); m != nil {
				s.Order, _ = strconv.Atoi(m[1])
				s.Slug = m[2]
				s.Title = humanize(m[2])
			}
			sections = append(sections, s)
			byDir[rel] = s
			return nil
		}
		if filepath.Ext(p) != ".md" || strings.HasPrefix(d.Name(), "_") || strings.EqualFold(d.Name(), "README.md") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		fm, body := splitFront(raw)
		var buf bytes.Buffer
		if err := md.Convert(body, &buf); err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		pg := &page{front: fm, Src: rel, Body: template.HTML(buf.String()), Text: plain(buf.String())}
		if pg.Title == "" {
			pg.Title = firstHeading(body, humanize(strings.TrimSuffix(d.Name(), ".md")))
		}
		dir := filepath.Dir(rel)
		name := strings.TrimSuffix(d.Name(), ".md")
		if dir == "." {
			if name == "index" {
				pg.URL = "/index.html"
				home = pg
			} else {
				pg.URL = "/" + name + ".html"
				// top-level loose pages hang off a synthetic section
				s := byDir["."]
				if s == nil {
					s = &section{Dir: ".", Slug: "", Title: "More", Order: 1 << 21}
					byDir["."] = s
					sections = append(sections, s)
				}
				pg.Section = s
				s.Pages = append(s.Pages, pg)
			}
			return nil
		}
		s := byDir[dir]
		pg.Section = s
		if name == "index" {
			pg.IsIndex = true
			pg.URL = "/" + s.Slug + "/index.html"
			s.Index = pg
			if fm.Title != "" {
				s.Title = fm.Title
			}
		} else {
			pg.URL = "/" + s.Slug + "/" + name + ".html"
		}
		s.Pages = append(s.Pages, pg)
		return nil
	})
	if err != nil {
		return err
	}
	if home == nil {
		return fmt.Errorf("missing %s/index.md", src)
	}

	sort.SliceStable(sections, func(i, j int) bool { return sections[i].Order < sections[j].Order })
	var all []*page
	all = append(all, home)
	for _, s := range sections {
		sort.SliceStable(s.Pages, func(i, j int) bool {
			a, b := s.Pages[i], s.Pages[j]
			if a.IsIndex != b.IsIndex {
				return a.IsIndex
			}
			if a.Order != b.Order {
				return a.Order < b.Order
			}
			return a.Src < b.Src
		})
		all = append(all, s.Pages...)
	}
	for i, p := range all {
		if i > 0 {
			p.Prev = all[i-1]
		}
		if i+1 < len(all) {
			p.Next = all[i+1]
		}
	}

	if err := os.RemoveAll(out); err != nil {
		return err
	}
	// assets
	if err := copyDir(filepath.Join(src, "assets"), filepath.Join(out, "assets")); err != nil {
		return err
	}
	// search index + llms.txt
	type idx struct {
		Title, URL, Section, Text string
	}
	var index []idx
	var llms strings.Builder
	llms.WriteString("# any docs\n\n")
	for _, p := range all {
		sec := ""
		if p.Section != nil {
			sec = p.Section.Title
		}
		index = append(index, idx{p.Title, p.URL, sec, truncate(p.Text, 4000)})
		fmt.Fprintf(&llms, "- [%s](%s)", p.Title, p.URL)
		if p.Description != "" {
			fmt.Fprintf(&llms, ": %s", p.Description)
		}
		llms.WriteString("\n")
	}
	ij, _ := json.Marshal(index)
	if err := os.WriteFile(filepath.Join(out, "search.json"), ij, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "llms.txt"), []byte(llms.String()), 0o644); err != nil {
		return err
	}
	// pages
	for _, p := range all {
		var buf bytes.Buffer
		err := tpl.Execute(&buf, map[string]any{
			"Page":     p,
			"Sections": sections,
			"Root":     rootPrefix(p.URL),
		})
		if err != nil {
			return fmt.Errorf("%s: %w", p.Src, err)
		}
		dst := filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(p.URL, "/")))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("anydocs: %d pages → %s\n", len(all), out)
	return nil
}

func rootPrefix(url string) string {
	depth := strings.Count(strings.TrimPrefix(url, "/"), "/")
	if depth == 0 {
		return "."
	}
	return strings.TrimSuffix(strings.Repeat("../", depth), "/")
}

func splitFront(raw []byte) (front, []byte) {
	var fm front
	if !bytes.HasPrefix(raw, []byte("---\n")) {
		return fm, raw
	}
	rest := raw[4:]
	end := bytes.Index(rest, []byte("\n---\n"))
	if end < 0 {
		return fm, raw
	}
	_ = yaml.Unmarshal(rest[:end], &fm)
	return fm, rest[end+5:]
}

func firstHeading(body []byte, fallback string) string {
	for _, l := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(l, "# ") {
			return strings.TrimSpace(l[2:])
		}
	}
	return fallback
}

func humanize(s string) string {
	s = strings.ReplaceAll(s, "-", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func plain(h string) string {
	return strings.TrimSpace(wsRe.ReplaceAllString(template.HTMLEscapeString(tagRe.ReplaceAllString(h, " ")), " "))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func copyDir(from, to string) error {
	if _, err := os.Stat(from); err != nil {
		return nil
	}
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}

const pageTpl = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Page.Title}} · any docs</title>
{{if .Page.Description}}<meta name="description" content="{{.Page.Description}}">{{end}}
<link rel="stylesheet" href="{{.Root}}/assets/site.css">
<link rel="icon" href="{{.Root}}/assets/favicon.svg">
</head>
<body>
<header class="top">
  <button class="menu" id="menu" aria-label="Menu">☰</button>
  <a class="brand" href="{{.Root}}/index.html"><span class="logo">any</span> docs</a>
  <div class="search"><input id="q" type="search" placeholder="Search docs… ( / )" autocomplete="off"><div id="results" class="results" hidden></div></div>
  <nav class="links"><a href="{{.Root}}/reference/http-api.html">API</a><a href="{{.Root}}/reference/cli.html">CLI</a><a href="https://github.com/anyproto/any">GitHub</a></nav>
</header>
<div class="shell">
<aside class="side" id="side">
{{$cur := .Page.URL}}{{$root := .Root}}
{{range .Sections}}
  <details class="sec"{{if or (eq $cur (printf "/%s/index.html" .Slug)) (hasPrefix $cur (printf "/%s/" .Slug))}} open{{end}}>
    <summary>{{.Title}}</summary>
    <ul>
    {{range .Pages}}<li><a href="{{$root}}{{.URL}}"{{if eq .URL $cur}} class="active" aria-current="page"{{end}}>{{if .IsIndex}}Overview{{else}}{{.Title}}{{end}}</a></li>
    {{end}}</ul>
  </details>
{{end}}
</aside>
<main class="main">
  <article class="doc">
    {{if .Page.Section}}<p class="crumb">{{.Page.Section.Title}}</p>{{end}}
    {{.Page.Body}}
  </article>
  <nav class="pager">
    {{if .Page.Prev}}<a class="prev" href="{{.Root}}{{.Page.Prev.URL}}"><small>Previous</small><span>{{.Page.Prev.Title}}</span></a>{{else}}<span></span>{{end}}
    {{if .Page.Next}}<a class="next" href="{{.Root}}{{.Page.Next.URL}}"><small>Next</small><span>{{.Page.Next.Title}}</span></a>{{end}}
  </nav>
  <footer class="foot">Source: <code>website/{{.Page.Src}}</code> · <a href="{{.Root}}/llms.txt">llms.txt</a></footer>
</main>
</div>
<script>window.__root={{.Root}};</script>
<script src="{{.Root}}/assets/site.js"></script>
</body>
</html>`

