package server

import (
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
)

// desktopShellOrigins are the webview origins of the desktop shell, the
// one CORS allowlist (routes.go): custom schemes web content cannot claim,
// plus the Vite dev origins.
var desktopShellOrigins = []string{
	"tauri://localhost",      // macOS / Linux webview
	"http://tauri.localhost", // Windows webview
	"http://localhost:5173",  // tauri dev (Vite, plain http)
	"http://127.0.0.1:5173",
}

// loopbackHostGuard refuses a request whose Host names anything but this
// machine by a name no DNS answer can change: `localhost` or a loopback
// IP literal. The listener is loopback-only, yet a browser reaches it
// under any name that resolves to 127.0.0.1 — a rebound attacker domain,
// or a public record pointing there — and a page under that name is
// same-origin with every route. Any port is accepted: a dev proxy
// forwards the page's own Host, whose port is not ours. An empty Host
// (HTTP/1.0) comes from no browser.
func loopbackHostGuard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !isLoopbackHost(c.Request().Host) {
			return writeError(c, http.StatusForbidden, "request.host_not_allowed",
				"the Host header must name this machine: localhost or a loopback address", nil)
		}
		return next(c)
	}
}

func isLoopbackHost(hostport string) bool {
	if hostport == "" {
		return true
	}
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// crossSiteWriteGuard refuses a write that a web page on another origin
// sent. CORS only hides the response: a "simple" cross-origin POST — a
// text/plain or form body, or none at all — still reaches the handler,
// so without this any website could join a space or mint an invite on
// the reader's server. A browser sends Origin on every request that is
// not GET or HEAD; the CLI, the mobile shims and the agent send none
// and pass untouched. Admitted: the desktop shell's origins and any
// loopback origin (a dev server, the `/ui` harness). The opaque `null`
// origin of a sandboxed document — what a file served by this server
// opens as — is refused.
func crossSiteWriteGuard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		r := c.Request()
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			return next(c)
		}
		origin := r.Header.Get(echo.HeaderOrigin)
		if origin == "" || isTrustedOrigin(origin) {
			return next(c)
		}
		return writeError(c, http.StatusForbidden, "request.origin_not_allowed",
			"a write from a web page must come from the desktop shell or a loopback origin", nil)
	}
}

func isTrustedOrigin(origin string) bool {
	if slices.Contains(desktopShellOrigins, origin) {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return u.Host != "" && isLoopbackHost(u.Host)
}
