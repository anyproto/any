package server

import (
	"bytes"
	"io"
	"testing"
)

// TestResolveMime pins the whole precedence ladder in one table: an
// explicit header wins, a binary signature beats the name, and within
// text the name beats the sniffer's guesses. The binary formats below
// are exactly the ones http.DetectContentType cannot place (svg lands
// on text/xml there, heic/mov/mp3 on octet-stream) — they are why this
// route sniffs with mimetype rather than the stdlib.
func TestResolveMime(t *testing.T) {
	// Real-world text that the sniffer's heuristics misfile: a README
	// opening with a badge block reads as HTML, prose with one comma
	// per line reads as CSV, an indented snippet as TSV.
	readme := []byte("<p align=\"center\"><img src=\"logo.png\"></p>\n\n# Project\n\nSome words.\n")
	commaProse := []byte("Hi Bob, see attached\nThanks, Alice\n")
	tabSnippet := []byte("a\tb\tc\n1\t2\t3\n")

	cases := []struct {
		name        string
		contentType string
		fileName    string
		head        []byte
		want        string
	}{
		// 1. an explicit header is final.
		{"explicit wins over content", "image/x-custom", "a.png", pngBytes, "image/x-custom"},
		{"explicit wins over name", "image/png", "notes.md", pngBytes, "image/png"},
		{"explicit params stripped", "text/plain; charset=utf-8", "", nil, "text/plain"},
		{"explicit with bad param keeps the type", "text/markdown; charset=", "", readme, "text/markdown"},
		{"unparseable header falls through", "not a mime", "", pngBytes, "image/png"},
		{"bare token is not a type", "png", "", pngBytes, "image/png"},
		// text/plain says "text", not which: the name refines it.
		{"text/plain header refined by name", "text/plain;charset=UTF-8", "notes.md", nil, "text/markdown"},
		{"text/plain header without a known name stays", "text/plain", "main.go", nil, "text/plain"},

		// 2. the "caller didn't say" spellings fall through to content.
		{"octet-stream sniffs", "application/octet-stream", "", pngBytes, "image/png"},
		{"binary octet-stream sniffs", "binary/octet-stream", "", pngBytes, "image/png"},
		{"application/unknown sniffs", "application/unknown", "", pngBytes, "image/png"},
		{"form-urlencoded (curl --data-binary) sniffs", "application/x-www-form-urlencoded", "photo.jpg", jpegBytes, "image/jpeg"},
		{"absent header sniffs", "", "", pngBytes, "image/png"},

		// 3. binary content the stdlib sniffer gets wrong or misses.
		{"svg", "", "logo.svg", svgBytes, "image/svg+xml"},
		{"heic", "", "IMG_0001.HEIC", heicBytes, "image/heic"},
		{"mov", "", "clip.mov", movBytes, "video/quicktime"},
		{"mp3 without id3", "", "song.mp3", mp3Bytes, "audio/mpeg"},
		{"jpeg", "", "", jpegBytes, "image/jpeg"},
		{"pdf", "", "", pdfBytes, "application/pdf"},
		{"name never overrides binary content", "", "fake.png", pdfBytes, "application/pdf"},

		// 4. within text the name decides.
		{"markdown by name", "", "notes.md", []byte("# Title\n\nbody\n"), "text/markdown"},
		{"markdown uppercase ext", "", "NOTES.MD", []byte("# Title\n\nbody\n"), "text/markdown"},
		{"markdown opening with html tags", "", "README.md", readme, "text/markdown"},
		{"prose with commas named .txt", "", "note.txt", commaProse, "text/plain"},
		{"list with commas named .md", "", "list.md", []byte("- Alice, Bob\n- Carol, Dave\n"), "text/markdown"},
		{"json named .txt", "", "data.txt", []byte(`{"a": 1}`), "text/plain"},
		{"plain text keeps text/plain", "", "notes.txt", []byte("hello, world\n"), "text/plain"},
		{"unmapped ext keeps text/plain", "", "main.go", []byte("package main\n"), "text/plain"},
		// Signature-less, extension-identified formats.
		{"css by name", "", "style.css", []byte("body { color: red }\n"), "text/css"},
		{"js by name", "", "app.js", []byte("console.log(1)\n"), "text/javascript"},
		{"xml without prolog by name", "", "data.xml", []byte("<root><a/></root>\n"), "text/xml"},
		{"svg without xmlns by name", "", "icon.svg", []byte("<svg><rect/></svg>"), "image/svg+xml"},
		{"yaml by name", "", "config.yaml", []byte("a: 1\n"), "application/yaml"},
		{"html by name", "", "page.html", []byte("<div>x</div>\n"), "text/html"},
		{"csv by name", "", "data.csv", commaProse, "text/csv"},

		// 5. no known extension: signature-backed text verdicts stand,
		// heuristic ones (html, csv, tsv) collapse to text/plain.
		{"json by content", "", "", []byte(`{"a": 1}`), "application/json"},
		{"xml prolog by content", "", "", []byte(`<?xml version="1.0"?><root/>`), "text/xml"},
		{"shebang by content", "", "run", []byte("#!/bin/sh\necho hi\n"), "text/x-shellscript"},
		{"html-looking text without a name", "", "", readme, "text/plain"},
		{"html-looking text with an unknown ext", "", "notes.log", readme, "text/plain"},
		{"comma prose without a name", "", "", commaProse, "text/plain"},
		{"tab snippet without a name", "", "pasted", tabSnippet, "text/plain"},

		// 6. nothing recognisable stays unset — never octet-stream.
		{"empty body", "", "x.png", nil, ""},
		{"unplaceable bytes", "", "x.png", bytes.Repeat([]byte{0x00, 0xff}, 300), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveMime(c.contentType, c.fileName, func() []byte { return c.head })
			if got != c.want {
				t.Errorf("resolveMime(%q, %q, %d bytes) = %q, want %q",
					c.contentType, c.fileName, len(c.head), got, c.want)
			}
		})
	}
}

// TestResolveMime_ExplicitHeaderNeverPeeks pins the streaming cost: a
// typed upload must not touch the body before the SDK does.
func TestResolveMime_ExplicitHeaderNeverPeeks(t *testing.T) {
	for _, ct := range []string{"image/png", "text/plain; charset=utf-8", "text/markdown; charset="} {
		resolveMime(ct, "notes.md", func() []byte {
			t.Errorf("peeked the body for Content-Type %q", ct)
			return nil
		})
	}
}

// TestPeekHead pins the streaming invariant: the sniff window must be
// readable without consuming it, for bodies both under and over the
// limit.
func TestPeekHead(t *testing.T) {
	cases := []struct {
		name     string
		in       []byte
		wantHead int
	}{
		{"empty", nil, 0},
		{"short", pngBytes, len(pngBytes)},
		{"exactly the limit", bytes.Repeat([]byte("a"), sniffLimit), sniffLimit},
		{"over the limit", bytes.Repeat([]byte("a"), sniffLimit*3), sniffLimit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, head := peekHead(bytes.NewReader(c.in))
			if len(head) != c.wantHead {
				t.Errorf("head = %d bytes, want %d", len(head), c.wantHead)
			}
			rest, err := io.ReadAll(r)
			if err != nil || !bytes.Equal(rest, c.in) {
				t.Errorf("body after peek: len %d err %v, want len %d", len(rest), err, len(c.in))
			}
		})
	}
}

// TestEnsureNameExt pins the fill-don't-rewrite rule: binary content
// gains a missing extension, text and anything already named are left
// alone.
func TestEnsureNameExt(t *testing.T) {
	cases := []struct{ name, mime, want string }{
		{"pasted", "image/png", "pasted.png"},
		{"pasted", "image/jpeg", "pasted.jpg"}, // canonical, not .jfif
		{"pasted", "application/pdf", "pasted.pdf"},
		{"shot.png", "image/png", "shot.png"},  // already has one
		{"shot.txt", "image/png", "shot.txt"},  // never rewritten
		{"pasted", "", "pasted"},               // mime unresolved
		{"", "image/png", ""},                  // no name to fill
		{"pasted", "image/x-custom", "pasted"}, // type the tree doesn't know
		// Text keeps its name, whatever the text.
		{"Makefile", "text/plain", "Makefile"},
		{"Dockerfile", "text/plain", "Dockerfile"},
		{"config", "application/json", "config"},
		{"notes", "text/markdown", "notes"},
		{"icon", "image/svg+xml", "icon"},
		// Dots that are not extensions.
		{"Screenshot 2026-09-01 at 10.32.11", "image/png", "Screenshot 2026-09-01 at 10.32.11.png"},
		{"v2.0", "application/pdf", "v2.0.pdf"},
		{"Report v1.2b", "application/pdf", "Report v1.2b"},
		{"foo.", "image/png", "foo."},
	}
	for _, c := range cases {
		if got := ensureNameExt(c.name, c.mime); got != c.want {
			t.Errorf("ensureNameExt(%q, %q) = %q, want %q", c.name, c.mime, got, c.want)
		}
	}
}
