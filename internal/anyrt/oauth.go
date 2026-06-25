package anyrt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	agentruntime "github.com/anyproto/anytype-agent-runtime/runtime"
)

// oauthDefaultTimeout bounds how long oauthFlow blocks waiting for the user
// to complete the consent screen and the loopback redirect to fire.
const oauthDefaultTimeout = 5 * time.Minute

// OAuthFlow is a host function (effect resolver) that runs the OAuth 2.0
// Authorization Code + PKCE flow for a NATIVE/installed app over a loopback
// redirect — the blessed pattern for desktop tools (cf. `gcloud auth login`).
// It is the one shared primitive every OAuth-class connector reuses; token
// REFRESH afterwards is a plain `fetch` POST in JS and needs no host fn.
//
// JS usage:
//
//	var t = oauthFlow({
//	  authUrl:      "https://accounts.google.com/o/oauth2/v2/auth",
//	  tokenUrl:     "https://oauth2.googleapis.com/token",
//	  clientId:     "...apps.googleusercontent.com",
//	  clientSecret: "...",            // optional for pure-PKCE providers
//	  scopes:       ["...gmail.readonly", "...calendar.readonly"], // array or space string
//	  authParams:   { access_type: "offline", prompt: "consent" }, // optional extras
//	  timeoutSec:   300               // optional, default 300
//	});
//	// -> { ok: true, accessToken, refreshToken, expiresAt, scope, tokenType }
//	// -> { ok: false, error }
//
// It opens the system browser to the consent URL (and logs the URL to stderr
// as a fallback), serves a tiny loopback page to capture the authorization
// code, validates `state`, then exchanges the code at tokenUrl.
func OAuthFlow(tr *agentruntime.TraceRecord, args ...any) any {
	fail := func(msg string) map[string]any {
		out := map[string]any{"ok": false, "error": msg}
		if tr != nil {
			b, _ := json.Marshal(out)
			tr.SetOutput("oauthFlow", string(b))
		}
		return out
	}

	if len(args) == 0 {
		return fail("oauthFlow: config object is required")
	}
	cfg, ok := args[0].(map[string]any)
	if !ok {
		return fail("oauthFlow: first argument must be a config object")
	}

	authURL := str(cfg["authUrl"])
	tokenURL := str(cfg["tokenUrl"])
	clientID := str(cfg["clientId"])
	clientSecret := str(cfg["clientSecret"])
	scope := scopeString(cfg["scopes"])
	if authURL == "" || tokenURL == "" || clientID == "" {
		return fail("oauthFlow: authUrl, tokenUrl and clientId are required")
	}

	timeout := oauthDefaultTimeout
	if s, ok := cfg["timeoutSec"].(float64); ok && s > 0 {
		timeout = time.Duration(s) * time.Second
	}

	// Loopback listener on a free port — Google & co. special-case
	// http://127.0.0.1:<any-port> for installed-app clients.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail("oauthFlow: cannot open loopback listener: " + err.Error())
	}
	defer ln.Close()
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port)

	// PKCE (S256) + anti-CSRF state.
	verifier, err := randToken(32)
	if err != nil {
		return fail("oauthFlow: rand: " + err.Error())
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state, err := randToken(16)
	if err != nil {
		return fail("oauthFlow: rand: " + err.Error())
	}

	// Build the authorization URL.
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	if scope != "" {
		q.Set("scope", scope)
	}
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	// Sensible defaults for getting a refresh token; caller can override.
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	if extra, ok := cfg["authParams"].(map[string]any); ok {
		for k, v := range extra {
			q.Set(k, str(v))
		}
	}
	consentURL := authURL + "?" + q.Encode()

	// Capture the redirect.
	type cbResult struct {
		code string
		err  string
	}
	resultCh := make(chan cbResult, 1)
	srv := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		qp := r.URL.Query()
		if e := qp.Get("error"); e != "" {
			writeCallbackPage(w, "Authorization failed: "+e+". You can close this tab.")
			resultCh <- cbResult{err: "consent denied: " + e}
			return
		}
		if qp.Get("state") != state {
			writeCallbackPage(w, "State mismatch — possible CSRF. You can close this tab.")
			resultCh <- cbResult{err: "state mismatch"}
			return
		}
		code := qp.Get("code")
		if code == "" {
			writeCallbackPage(w, "No authorization code received. You can close this tab.")
			resultCh <- cbResult{err: "no code in callback"}
			return
		}
		writeCallbackPage(w, "Connected! You can close this tab and return to bobrik.")
		resultCh <- cbResult{code: code}
	})
	srv.Handler = mux
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// Open the browser; log the URL as a fallback (we block before JS could
	// surface it, so stderr is the user's manual path).
	fmt.Fprintln(os.Stderr, "[oauthFlow] open this URL to authorize:\n  "+consentURL)
	_ = openBrowser(consentURL)

	var code string
	select {
	case res := <-resultCh:
		if res.err != "" {
			return fail("oauthFlow: " + res.err)
		}
		code = res.code
	case <-time.After(timeout):
		return fail("oauthFlow: timed out waiting for authorization (no callback within " + timeout.String() + ")")
	}

	// Exchange the code for tokens.
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", clientID)
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	form.Set("code_verifier", verifier)

	httpCli := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpCli.PostForm(tokenURL, form)
	if err != nil {
		return fail("oauthFlow: token exchange request failed: " + err.Error())
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fail(fmt.Sprintf("oauthFlow: token exchange HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}

	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return fail("oauthFlow: cannot parse token response: " + err.Error())
	}
	if tok.AccessToken == "" {
		return fail("oauthFlow: token response had no access_token: " + strings.TrimSpace(string(body)))
	}

	out := map[string]any{
		"ok":           true,
		"accessToken":  tok.AccessToken,
		"refreshToken": tok.RefreshToken,
		"expiresAt":    time.Now().Unix() + tok.ExpiresIn,
		"scope":        tok.Scope,
		"tokenType":    tok.TokenType,
	}
	if tr != nil {
		// Don't trace token material; record only success.
		tr.SetOutput("oauthFlow", `{"ok":true}`)
	}
	return out
}

// str coerces a JS-passed value to string (values arrive as string/float64/etc.).
func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

// scopeString accepts either an array of scope strings or a single
// space-delimited string and returns the space-joined form.
func scopeString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, s := range x {
			if ss := str(s); ss != "" {
				parts = append(parts, ss)
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writeCallbackPage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><html><body style=\"font-family:sans-serif;padding:3rem;text-align:center\"><h2>%s</h2></body></html>", msg)
}

// openBrowser best-effort opens a URL in the user's default browser.
func openBrowser(target string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{target}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", target}
	default: // linux, bsd, ...
		cmd, args = "xdg-open", []string{target}
	}
	return exec.Command(cmd, args...).Start()
}
