package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// webappFS holds the built SPA assets produced by `make web`.
//
// The directory is gitignored; the .gitkeep at webapp/dist/ keeps the
// path resolvable when no build has run yet — without it `//go:embed`
// would fail to compile. CI populates this directory before
// `go build`. See docs/08-app-architecture.md § "Build / ship pipeline".
//
//go:embed all:webapp/dist
var webappFS embed.FS

// registerWebappRoutes mounts the SPA at GET /ui and any unknown path
// under /ui/*. Built assets (which Vite emits under /assets/...) are
// served from /ui/assets/* with long-cache headers; index.html is
// served with no-cache so a redeploy is picked up immediately.
//
// Same-origin stance from docs/00-overview.md is preserved — the SPA
// fetches /v1/... on the same host:port, no CORS.
func registerWebappRoutes(e *echo.Echo) {
	dist, err := fs.Sub(webappFS, "webapp/dist")
	if err != nil {
		// embed.FS guarantees this path exists at compile time. Bail loudly.
		panic("webapp/dist embed missing: " + err.Error())
	}

	// Hashed assets: `/ui/assets/index-AbCd1234.js`.
	// Cache-Control immutable so the browser doesn't re-fetch. Vite
	// emits hashed names, so a content change always changes the URL.
	assetsServer := http.StripPrefix("/ui", http.FileServer(http.FS(dist)))
	e.GET("/ui/assets/*", func(c echo.Context) error {
		c.Response().Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		assetsServer.ServeHTTP(c.Response(), c.Request())
		return nil
	})

	// Favicon, manifest, and any other public asset Vite copied to
	// the dist root (non-hashed). Same StripPrefix.
	e.GET("/ui/*", func(c echo.Context) error {
		path := strings.TrimPrefix(c.Request().URL.Path, "/ui/")
		// Only serve real files; everything else falls through to the
		// SPA index handler below.
		if path != "" && fileExists(dist, path) {
			c.Response().Header().Set("Cache-Control", "no-cache")
			assetsServer.ServeHTTP(c.Response(), c.Request())
			return nil
		}
		return serveIndex(c, dist)
	})

	// SPA root: /ui and /ui/.
	indexHandler := func(c echo.Context) error { return serveIndex(c, dist) }
	e.GET("/ui", indexHandler)
	e.GET("/ui/", indexHandler)
}

func fileExists(dist fs.FS, name string) bool {
	f, err := dist.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return !stat.IsDir()
}

func serveIndex(c echo.Context, dist fs.FS) error {
	indexBytes, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		// Empty dist (no `make web` yet). Serve a placeholder so
		// `any run` still works for Go-side development.
		c.Response().Header().Set("Cache-Control", "no-cache")
		return c.HTMLBlob(http.StatusOK, []byte(spaPlaceholder))
	}
	c.Response().Header().Set("Cache-Control", "no-cache")
	return c.HTMLBlob(http.StatusOK, indexBytes)
}

const spaPlaceholder = `<!doctype html>
<title>any — webapp not built</title>
<style>body{font:14px system-ui;margin:2rem;max-width:36rem;color:#333}code{background:#eee;padding:.1em .3em;border-radius:3px}</style>
<h1>SPA assets not built</h1>
<p>Run <code>make web</code> from the repo root, then rebuild the binary:</p>
<pre><code>make web && go build ./cmd/any && ./any run</code></pre>
<p>The legacy dev harness is still available at <a href="/ui-legacy">/ui-legacy</a>.</p>
`
