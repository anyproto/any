package server

import (
	"bufio"
	"io"
	"mime"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

// The mime stored with an upload is resolved once, at attach, from the
// three signals the route has — header, content, name — and download
// serves whatever attach stored, never sniffing. Wire contract:
// docs/03-api.md § Files (Mime precedence).

// sniffLimit is the window the content sniffer reads. It is mimetype's
// own default (its unexported defaultLimit — recheck on a mimetype
// bump), and init pins the library to it: a peek window shorter than the limit the detectors
// assume does not fail loudly — the ones that check whether the tail
// is truncated (csv, tsv) or parse the OLE directory (doc, xls, msi)
// misclassify silently.
const sniffLimit = 4096

func init() { mimetype.SetLimit(sniffLimit) }

// unsetContentTypes are the Content-Type spellings a tool sends when
// the caller did not name a type — curl -T, fetch() with a typeless
// Blob, the S3-flavoured variants, and the form default curl
// --data-binary falls back to. None of them can be the type of a
// file, so they fall through to the content.
var unsetContentTypes = map[string]struct{}{
	"application/octet-stream":          {},
	"binary/octet-stream":               {},
	"application/unknown":               {},
	"application/x-www-form-urlencoded": {},
}

// textByExt maps the extensions of the common text formats to their
// types. Within text the name is the strongest signal: text formats
// carry no signature, only conventions the sniffer guesses at — a
// markdown README that opens with a badge <p> matches the HTML tag
// list, two lines with a comma each match the CSV field-count rule.
// A known extension therefore decides, whatever the sniffer's text
// sub-verdict.
//
// A fixed table rather than mime.TypeByExtension: the stdlib seeds
// that from the host's /etc/mime.types at init, so the same upload
// would resolve differently on two machines (".ts" answers
// text/vnd.trolltech.linguist wherever Qt is installed). A server has
// to decide identically everywhere.
var textByExt = map[string]string{
	".txt":      "text/plain",
	".md":       "text/markdown",
	".markdown": "text/markdown",
	".csv":      "text/csv",
	".tsv":      "text/tab-separated-values",
	".html":     "text/html",
	".htm":      "text/html",
	".css":      "text/css",
	".js":       "text/javascript",
	".mjs":      "text/javascript",
	".json":     "application/json",
	".xml":      "text/xml",
	".svg":      "image/svg+xml",
	".yaml":     "application/yaml",
	".yml":      "application/yaml",
}

// weakTextTypes are the sniffer's text sub-verdicts that rest on a
// heuristic rather than a signature — the WHATWG tag list for HTML,
// consistent field counts for CSV and TSV. They stand only when the
// name carries the matching extension; otherwise the content is plain
// text as far as attach can tell.
var weakTextTypes = map[string]struct{}{
	"text/html":                 {},
	"text/csv":                  {},
	"text/tab-separated-values": {},
}

// resolveMime decides the stored mime, strongest signal first:
//
//  1. an explicit Content-Type — a caller naming its own content is
//     final (HTTP's own rule). Only the unsetContentTypes spellings and
//     a type that does not parse fall through; a malformed parameter
//     after a good type does not void it. text/plain is the one header
//     the name refines (rule 3): it is what fetch() sends for a string
//     body, and it says "text", not which text,
//  2. the content — a binary signature beats everything else, so a
//     file *named* .png that *is* a PDF stores application/pdf,
//  3. the name's extension, within text only — see textByExt and
//     weakTextTypes.
//
// Content the sniffer cannot place leaves the mime unset ("") rather
// than asserting application/octet-stream: "absent" and "unknown" are
// different claims, and the download route already falls back.
//
// peek yields the sniff window and runs only when the header has not
// settled the question, so a typed upload never pays the buffered
// read and the SDK's preflight rejections (space, object, variant)
// stay ahead of the first body byte.
func resolveMime(contentType, name string, peek func() []byte) string {
	ext := strings.ToLower(filepath.Ext(name))
	if mt := mediaType(contentType); mt != "" {
		if mt == "text/plain" {
			if refined, ok := textByExt[ext]; ok {
				return refined
			}
		}
		return mt
	}
	head := peek()
	if len(head) == 0 {
		// Nothing to sniff — and the text detector would accept it.
		return ""
	}
	m := mimetype.Detect(head)
	if m.Parent() == nil {
		// The tree's root: no detector matched.
		return ""
	}
	if !isText(m) {
		return bareType(m.String())
	}
	if refined, ok := textByExt[ext]; ok {
		return refined
	}
	mt := bareType(m.String())
	if _, weak := weakTextTypes[mt]; weak {
		return "text/plain"
	}
	return mt
}

// isText reports whether m descends from text/plain in the sniffer's
// tree — html, csv, json, xml, svg and every other text format share
// that ancestor.
func isText(m *mimetype.MIME) bool {
	for ; m != nil; m = m.Parent() {
		if m.Is("text/plain") {
			return true
		}
	}
	return false
}

// mediaType strips parameters (charset and friends) off a content
// type and maps the "caller didn't say" spellings — plus anything
// that is not a type/subtype at all — to "". A bad parameter after a
// good type keeps the type: mime.ParseMediaType still returns it.
func mediaType(contentType string) string {
	mt := bareType(contentType)
	if !strings.Contains(mt, "/") {
		return ""
	}
	if _, unset := unsetContentTypes[mt]; unset {
		return ""
	}
	return mt
}

// bareType is the type/subtype of a media type string, parameters
// dropped; "" when the type itself does not parse.
func bareType(s string) string {
	mt, _, _ := mime.ParseMediaType(s)
	return mt
}

// peekHead buffers the first sniffLimit bytes of r without consuming
// them: it returns a reader that still yields the whole body, plus the
// window the sniffer gets to look at. Peeking is what keeps attach
// streaming — sniffing by reading and seeking back would mean making
// the body seekable, i.e. buffering the whole upload to memory or a
// temp file before a single byte reaches the SDK.
func peekHead(r io.Reader) (io.Reader, []byte) {
	br := bufio.NewReaderSize(r, sniffLimit)
	// A short read at EOF is the normal small-file case; a real read
	// error resurfaces when Attach consumes the body, which is where
	// it belongs.
	head, _ := br.Peek(sniffLimit)
	return br, head
}

// ensureNameExt gives a name without an extension the one its mime
// implies, so a pasted screenshot arriving as ?name=pasted downloads
// as pasted.png instead of a file the OS opens with a shrug. Binary
// content only: text opens under any name, and extension-less text
// names are a convention (Makefile, Dockerfile, LICENSE) the store
// must hand back unchanged. An extension the caller supplied is never
// rewritten, and a mime the sniffer's tree does not know cannot name
// one.
func ensureNameExt(name, mimeType string) string {
	if name == "" || mimeType == "" || hasExt(name) {
		return name
	}
	m := mimetype.Lookup(mimeType)
	if m == nil || isText(m) {
		return name
	}
	if ext, ok := extByMime[m.String()]; ok {
		return name + ext
	}
	// Extension() is the canonical single answer (.jpg), not the list
	// mime.ExtensionsByType sorts .jfif to the front of.
	return name + m.Extension()
}

// extByMime overrides the sniffer's canonical extension where it is one
// few systems associate: an animated PNG is a PNG every viewer opens,
// but .apng is not.
var extByMime = map[string]string{
	"image/apng": ".png",
}

// hasExt reports whether name ends in something that reads as a file
// extension: a dot followed by a short alphanumeric run with at least
// one letter. filepath.Ext alone takes "Screenshot at 10.32.11" and
// "v2.0" for extensions — exactly the paste names this fills for. A
// trailing dot counts as one: the name is odd, not missing anything.
func hasExt(name string) bool {
	ext := filepath.Ext(name)
	if ext == "" {
		return false
	}
	if ext == "." {
		return true
	}
	s := ext[1:]
	if len(s) > 10 {
		return false
	}
	letter := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			letter = true
		case r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return letter
}
