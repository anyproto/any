// __main_source
// __tags: integration
// googleAuth@v1 — shared Google OAuth layer for the gmail / googleSheets /
// googleCalendar / googleDrive connectors. ONE OAuth client, ONE consent,
// ONE refresh token covers all four (incremental authorization over the union
// of scopes). The initial consent runs through the `oauthFlow` host function
// (auth-code + PKCE over a loopback redirect — internal/anyrt/oauth.go);
// afterwards token refresh is a plain `fetch` POST and needs no host fn.
//
// As a TOOL it exposes connect() / status(). As a MODULE it exports token() and
// authedFetch() — the connectors import those and never touch OAuth directly.
// Contract: dev space → Integration Docs → 00 (foundation) + 01/03/04/05.
import { main as getConfig } from "config@v1";

var AUTH_URL = "https://accounts.google.com/o/oauth2/v2/auth";
var TOKEN_URL = "https://oauth2.googleapis.com/token";

// Default scope union — all SENSITIVE (not restricted), so no CASA assessment.
// The Drive Doc-export fallback needs the RESTRICTED drive.readonly scope; add
// it here only if you accept Google's restricted-scope verification (see the
// googleDrive plan). meetings.space.readonly powers Meet transcripts.
var DEFAULT_SCOPES = [
  "openid",
  "email",
  "https://www.googleapis.com/auth/gmail.readonly",
  "https://www.googleapis.com/auth/spreadsheets.readonly",
  "https://www.googleapis.com/auth/drive.metadata.readonly",
  "https://www.googleapis.com/auth/calendar.readonly",
  "https://www.googleapis.com/auth/meetings.space.readonly"
];

// Session cache of the current access token (Date.now() is available in the
// runtime). The refresh token persists in config@v1; we also keep it in memory
// for the rest of the process after connect().
var _accessToken = "";
var _accessExpMs = 0;
var _memRefresh = "";

function _cfg() { return getConfig(); }

function _refreshToken() {
  return _memRefresh || _cfg().GOOGLE_OAUTH_REFRESH_TOKEN || "";
}

function _formEncode(obj) {
  var parts = [];
  for (var k in obj) {
    if (Object.prototype.hasOwnProperty.call(obj, k)) {
      parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(obj[k]));
    }
  }
  return parts.join("&");
}

// Exchange the stored refresh token for a fresh access token. Caches it with a
// 60s safety margin. Throws a clean Error on failure.
function refresh() {
  var cfg = _cfg();
  var clientId = cfg.GOOGLE_OAUTH_CLIENT_ID;
  var clientSecret = cfg.GOOGLE_OAUTH_CLIENT_SECRET;
  var rt = _refreshToken();
  if (!clientId || !clientSecret) {
    throw new Error("google not configured — set GOOGLE_OAUTH_CLIENT_ID / GOOGLE_OAUTH_CLIENT_SECRET in config@v1 (Google Cloud Console → Credentials → OAuth client, type Desktop app).");
  }
  if (!rt) {
    throw new Error("google not connected — run googleAuth.connect() once, then save the returned refresh token to config@v1 as GOOGLE_OAUTH_REFRESH_TOKEN.");
  }
  var resp = fetch(TOKEN_URL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: _formEncode({
      client_id: clientId,
      client_secret: clientSecret,
      refresh_token: rt,
      grant_type: "refresh_token"
    })
  });
  if (!resp || !resp.ok) {
    var detail = (resp && resp.body && (resp.body.error_description || resp.body.error)) || ("HTTP " + (resp ? resp.status : "?"));
    throw new Error("google token refresh failed: " + detail + " (the refresh token may be revoked/expired — re-run googleAuth.connect()).");
  }
  _accessToken = resp.body.access_token;
  _accessExpMs = Date.now() + ((resp.body.expires_in || 3600) * 1000) - 60000;
  return _accessToken;
}

// token() — a valid access token, refreshing if the cached one is stale.
export function token() {
  if (_accessToken && Date.now() < _accessExpMs) return _accessToken;
  return refresh();
}

// authedFetch(url, opts) — fetch with a Bearer access token injected. On a 401
// it refreshes once and retries (covers a token that expired mid-session or a
// fresh process whose cache is empty). Connectors use this for every call.
export function authedFetch(url, opts) {
  opts = opts || {};
  opts.headers = opts.headers || {};
  opts.headers.Authorization = "Bearer " + token();
  var resp = fetch(url, opts);
  if (resp && resp.status === 401) {
    refresh();
    opts.headers.Authorization = "Bearer " + _accessToken;
    resp = fetch(url, opts);
  }
  return resp;
}

// getJSON(url) — authed GET returning {ok, body}/{ok:false,error}. Convenience
// for the read-only connectors.
export function getJSON(url) {
  var resp = authedFetch(url, { method: "GET", headers: { "Accept": "application/json" } });
  if (!resp || !resp.ok) {
    var detail = (resp && resp.body && resp.body.error && (resp.body.error.message || resp.body.error)) || ("HTTP " + (resp ? resp.status : "?"));
    return { ok: false, error: detail, status: resp && resp.status };
  }
  return { ok: true, body: resp.body, headers: resp.headers };
}

// --- tool methods ---------------------------------------------------------

// connect(opts?) — run the one-time Google consent. Opens the browser to the
// consent screen and blocks until you approve. Returns the refresh token; SAVE
// it to config@v1 as GOOGLE_OAUTH_REFRESH_TOKEN so it survives restarts.
export function connect(opts) {
  opts = opts || {};
  var cfg = _cfg();
  if (!cfg.GOOGLE_OAUTH_CLIENT_ID || !cfg.GOOGLE_OAUTH_CLIENT_SECRET) {
    return { ok: false, error: "Set GOOGLE_OAUTH_CLIENT_ID and GOOGLE_OAUTH_CLIENT_SECRET in config@v1 first (Google Cloud Console → APIs & Services → Credentials → OAuth client ID, application type 'Desktop app')." };
  }
  if (typeof oauthFlow === "undefined") {
    return { ok: false, error: "oauthFlow host function unavailable — rebuild `any`/bobrik-watch with the OAuth host fn (internal/anyrt/oauth.go)." };
  }
  var scopes = opts.scopes || DEFAULT_SCOPES;
  var res = oauthFlow({
    authUrl: AUTH_URL,
    tokenUrl: TOKEN_URL,
    clientId: cfg.GOOGLE_OAUTH_CLIENT_ID,
    clientSecret: cfg.GOOGLE_OAUTH_CLIENT_SECRET,
    scopes: scopes,
    authParams: { access_type: "offline", prompt: "consent", include_granted_scopes: "true" }
  });
  if (!res || !res.ok) {
    return { ok: false, error: (res && res.error) || "oauthFlow failed" };
  }
  _accessToken = res.accessToken;
  _accessExpMs = Date.now() + 3000000; // ~50min; refresh() recomputes precisely
  if (res.refreshToken) _memRefresh = res.refreshToken;
  return {
    ok: true,
    connected: true,
    refreshToken: res.refreshToken || "",
    scope: res.scope || scopes.join(" "),
    note: res.refreshToken
      ? "Connected. SAVE this refreshToken to config@v1 as GOOGLE_OAUTH_REFRESH_TOKEN so it survives a restart (ask anyPrograms to edit config@v1)."
      : "Connected for this session, but Google returned no refresh token — revoke the app's access at https://myaccount.google.com/permissions and re-run connect() to force a fresh consent."
  };
}

// status() — whether Google is configured/connected.
export function status() {
  var cfg = _cfg();
  return {
    ok: true,
    configured: !!(cfg.GOOGLE_OAUTH_CLIENT_ID && cfg.GOOGLE_OAUTH_CLIENT_SECRET),
    connected: !!_refreshToken(),
    hasSessionToken: !!_accessToken,
    scopes: DEFAULT_SCOPES
  };
}

export function main() { return status(); }
