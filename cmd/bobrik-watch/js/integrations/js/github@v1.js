// __main_source
// __tags: integration
// github@v1 — read the user's GitHub work context (issues, pull requests,
// notifications, repo README/contents) into bobrik. Read-first, no writes.
// Pattern-1 token connector: reads cfg.GITHUB_TOKEN from config@v1 (a
// fine-grained Personal Access Token). Wraps the REST API at
// https://api.github.com with a shared ghFetch helper that sets the four
// required headers, checks status, follows the Link header for pagination,
// and backs off on rate-limit 403/429 (honoring Retry-After). Every method
// returns a consistent {ok, ...} / {ok:false, error} shape and trims
// GitHub's huge payloads down to small objects so the agent's context isn't
// flooded. See dev space → Integration Docs → github.

import { main as getConfig } from "config@v1";

var BASE = "https://api.github.com";
var API_VERSION = "2022-11-28";
var USER_AGENT = "bobrik-github@v1";
var TOKEN_URL = "https://github.com/settings/tokens?type=beta";
var MAX_RETRIES = 3;

// --- credential ---

function _token() {
  var cfg = getConfig();
  return cfg && cfg.GITHUB_TOKEN;
}

function _notConnected() {
  return {
    ok: false,
    error: "GitHub not connected — create a fine-grained Personal Access Token at "
      + TOKEN_URL + " with Read-only permissions (Repository: Issues, Pull requests, "
      + "Contents, Metadata; Account: Notifications if you want listNotifications), "
      + "then add it to config@v1 as GITHUB_TOKEN (or paste it to me and I'll save it)."
  };
}

function _headers(token, accept) {
  return {
    "Authorization": "Bearer " + token,
    "Accept": accept || "application/vnd.github+json",
    "X-GitHub-Api-Version": API_VERSION,
    "User-Agent": USER_AGENT
  };
}

// --- http ---

// _hdr — case-insensitive single response-header read (headers is a plain map).
function _hdr(resp, name) {
  if (!resp || !resp.headers) return null;
  var h = resp.headers;
  var lower = name.toLowerCase();
  var k;
  for (k in h) {
    if (h.hasOwnProperty(k) && k.toLowerCase() === lower) return h[k];
  }
  return null;
}

// _errorFrom — turn a non-ok response into a clear {ok:false,error,status}.
// 401 = bad/expired token (re-prompt); 403/429 with a rate-limit signal =
// throttled; other 403 = a missing permission on the PAT.
function _errorFrom(resp) {
  var status = resp ? resp.status : 0;
  if (status === 401) {
    return { ok: false, status: 401, error: "GitHub token invalid or expired — paste a "
      + "fresh fine-grained PAT (create one at " + TOKEN_URL + ")." };
  }
  var remaining = _hdr(resp, "x-ratelimit-remaining");
  var rateLimited = (status === 429) || (status === 403 && remaining === "0");
  if (rateLimited) {
    return { ok: false, status: status, error: "GitHub rate limit hit — back off and "
      + "retry later (search is limited to 30/min; other reads 5000/hr)." };
  }
  var body = resp && resp.body;
  var apiMsg = body && body.message ? body.message : ("HTTP " + status);
  if (status === 403) {
    return { ok: false, status: 403, error: "GitHub returned 403 (likely a missing PAT "
      + "permission or org SSO approval): " + apiMsg };
  }
  return { ok: false, status: status, error: "GitHub error: " + apiMsg };
}

// ghFetch — one request with bounded rate-limit backoff. Returns the raw
// fetch response on success, or a {ok:false,...} error object.
function ghFetch(token, path, accept) {
  var url = path.indexOf("http") === 0 ? path : (BASE + path);
  var attempt = 0;
  while (attempt <= MAX_RETRIES) {
    var resp = fetch(url, { method: "GET", headers: _headers(token, accept) });
    if (resp && resp.ok) return resp;
    var status = resp ? resp.status : 0;
    var remaining = _hdr(resp, "x-ratelimit-remaining");
    var rateLimited = (status === 429) || (status === 403 && remaining === "0");
    if (rateLimited && attempt < MAX_RETRIES) {
      var retryAfter = _hdr(resp, "retry-after");
      var waitMs = retryAfter ? (parseInt(retryAfter, 10) * 1000) : (1000 * (attempt + 1));
      if (!waitMs || waitMs < 0) waitMs = 1000;
      if (waitMs > 60000) waitMs = 60000;
      sleep(waitMs);
      attempt++;
      continue;
    }
    return _errorFrom(resp);
  }
  return { ok: false, error: "GitHub rate limit — gave up after " + MAX_RETRIES + " retries." };
}

// _isErr — distinguish an error object from a real fetch response.
function _isErr(x) {
  return x && x.ok === false;
}

// _nextLink — parse the Link header for rel="next".
function _nextLink(resp) {
  var link = _hdr(resp, "link");
  if (!link) return null;
  var parts = link.split(",");
  var i;
  for (i = 0; i < parts.length; i++) {
    var seg = parts[i];
    if (seg.indexOf('rel="next"') !== -1) {
      var lt = seg.indexOf("<");
      var gt = seg.indexOf(">");
      if (lt !== -1 && gt !== -1 && gt > lt) return seg.substring(lt + 1, gt);
    }
  }
  return null;
}

// _paged — follow Link rel="next" accumulating array bodies up to maxItems.
function _paged(token, path, maxItems) {
  var out = [];
  var url = path;
  var guard = 0;
  while (url && out.length < maxItems && guard < 20) {
    guard++;
    var resp = ghFetch(token, url);
    if (_isErr(resp)) return resp;
    var arr = resp.body;
    if (!Array.isArray(arr)) return { ok: false, error: "unexpected GitHub response (not a list)" };
    var i;
    for (i = 0; i < arr.length && out.length < maxItems; i++) out.push(arr[i]);
    url = _nextLink(resp);
  }
  return { ok: true, items: out };
}

// --- shaping ---

function _clamp(n, def, max) {
  n = parseInt(n, 10);
  if (!n || n < 1) n = def;
  if (n > max) n = max;
  return n;
}

function _trim(text, max) {
  if (typeof text !== "string") return "";
  if (text.length <= max) return text;
  return text.substring(0, max) + "\n…[truncated]";
}

function _repoFullName(item) {
  if (item.repository && item.repository.full_name) return item.repository.full_name;
  if (item.repository_url) {
    var m = item.repository_url.replace(BASE + "/repos/", "");
    return m;
  }
  return null;
}

// _issue — normalize a GitHub issue/PR payload down to the small Issue shape.
function _issue(item) {
  return {
    number: item.number,
    title: item.title,
    state: item.state,
    url: item.html_url,
    repo: _repoFullName(item),
    author: item.user ? item.user.login : null,
    labels: _labelNames(item.labels),
    isPR: !!item.pull_request,
    updatedAt: item.updated_at,
    body: _trim(item.body, 2000)
  };
}

function _labelNames(labels) {
  var out = [];
  if (!Array.isArray(labels)) return out;
  var i;
  for (i = 0; i < labels.length; i++) {
    var l = labels[i];
    out.push(typeof l === "string" ? l : (l && l.name));
  }
  return out;
}

function _issues(arr) {
  var out = [];
  var i;
  for (i = 0; i < arr.length; i++) out.push(_issue(arr[i]));
  return out;
}

function _comment(c) {
  return {
    author: c.user ? c.user.login : null,
    createdAt: c.created_at,
    body: _trim(c.body, 1500)
  };
}

function _b64decode(content) {
  // GitHub returns base64 with embedded newlines; strip them, then decode.
  var clean = (content || "").replace(/\n/g, "").replace(/\r/g, "");
  if (typeof atob === "function") {
    try { return atob(clean); } catch (e) { /* fall through */ }
  }
  // Manual base64 decode (Sobek has no Buffer); UTF-8 unaware but adequate
  // for README/source text — multibyte sequences may render imperfectly.
  return _b64fallback(clean);
}

var _B64CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
function _b64fallback(str) {
  str = str.replace(/=+$/, "");
  var out = "";
  var bits = 0;
  var acc = 0;
  var i;
  for (i = 0; i < str.length; i++) {
    var idx = _B64CHARS.indexOf(str.charAt(i));
    if (idx === -1) continue;
    acc = (acc << 6) | idx;
    bits += 6;
    if (bits >= 8) {
      bits -= 8;
      out += String.fromCharCode((acc >> bits) & 0xff);
    }
  }
  return out;
}

function _enc(s) {
  return encodeURIComponent(String(s));
}

function _qs(params) {
  var parts = [];
  var k;
  for (k in params) {
    if (params.hasOwnProperty(k) && params[k] !== undefined && params[k] !== null && params[k] !== "") {
      parts.push(_enc(k) + "=" + _enc(params[k]));
    }
  }
  return parts.length ? ("?" + parts.join("&")) : "";
}

// --- tool methods (each maps 1:1 to a ## Tool Schema entry) ---

// whoami() [getter] — verify the stored token, return the authenticated user.
export function whoami() {
  var token = _token();
  if (!token) return _notConnected();
  var resp = ghFetch(token, "/user");
  if (_isErr(resp)) return resp;
  var u = resp.body;
  return { ok: true, login: u.login, id: u.id, name: u.name, url: u.html_url };
}

// listMyIssues(opts) [getter] — issues + PRs assigned/created/mentioning the user.
export function listMyIssues(opts) {
  opts = opts || {};
  var token = _token();
  if (!token) return _notConnected();
  var perPage = _clamp(opts.perPage, 30, 100);
  var path = "/issues" + _qs({
    filter: opts.filter || "assigned",
    state: opts.state || "open",
    since: opts.since,
    per_page: perPage,
    sort: "updated",
    direction: "desc"
  });
  var res = _paged(token, path, perPage);
  if (_isErr(res)) return res;
  return { ok: true, items: _issues(res.items) };
}

// listRepoIssues(owner, repo, opts) [getter] — one repo's issues + PRs.
export function listRepoIssues(owner, repo, opts) {
  opts = opts || {};
  if (!owner || !repo) return { ok: false, error: "owner and repo are required" };
  var token = _token();
  if (!token) return _notConnected();
  var perPage = _clamp(opts.perPage, 30, 100);
  var path = "/repos/" + _enc(owner) + "/" + _enc(repo) + "/issues" + _qs({
    state: opts.state || "open",
    since: opts.since,
    per_page: perPage,
    sort: "updated",
    direction: "desc"
  });
  var res = _paged(token, path, perPage);
  if (_isErr(res)) return res;
  return { ok: true, items: _issues(res.items) };
}

// searchIssues(q, opts) [getter] — targeted search (30/min budget — use sparingly).
export function searchIssues(q, opts) {
  opts = opts || {};
  if (!q) return { ok: false, error: "q (search query) is required" };
  var token = _token();
  if (!token) return _notConnected();
  var perPage = _clamp(opts.perPage, 30, 100);
  var path = "/search/issues" + _qs({ q: q, per_page: perPage });
  var resp = ghFetch(token, path);
  if (_isErr(resp)) return resp;
  var data = resp.body;
  var items = Array.isArray(data.items) ? data.items : [];
  return { ok: true, totalCount: data.total_count, incompleteResults: data.incomplete_results, items: _issues(items) };
}

// getIssue(owner, repo, number) [getter] — one issue/PR with its comments.
export function getIssue(owner, repo, number) {
  if (!owner || !repo) return { ok: false, error: "owner and repo are required" };
  if (number === undefined || number === null) return { ok: false, error: "number is required" };
  var token = _token();
  if (!token) return _notConnected();
  var base = "/repos/" + _enc(owner) + "/" + _enc(repo) + "/issues/" + _enc(number);
  var resp = ghFetch(token, base);
  if (_isErr(resp)) return resp;
  var issue = _issue(resp.body);
  var cRes = _paged(token, base + "/comments" + _qs({ per_page: 100 }), 100);
  var comments = [];
  if (!_isErr(cRes)) {
    var i;
    for (i = 0; i < cRes.items.length; i++) comments.push(_comment(cRes.items[i]));
  }
  return { ok: true, issue: issue, comments: comments };
}

// listNotifications(opts) [getter] — the authenticated user's notification inbox.
export function listNotifications(opts) {
  opts = opts || {};
  var token = _token();
  if (!token) return _notConnected();
  var perPage = _clamp(opts.perPage, 30, 100);
  var path = "/notifications" + _qs({
    all: opts.all ? "true" : "false",
    since: opts.since,
    per_page: perPage
  });
  var res = _paged(token, path, perPage);
  if (_isErr(res)) return res;
  var out = [];
  var i;
  for (i = 0; i < res.items.length; i++) {
    var n = res.items[i];
    var subject = n.subject || {};
    out.push({
      id: n.id,
      reason: n.reason,
      subjectTitle: subject.title,
      type: subject.type,
      url: subject.url,
      repo: n.repository ? n.repository.full_name : null,
      updatedAt: n.updated_at,
      unread: n.unread
    });
  }
  return { ok: true, items: out };
}

// getReadme(owner, repo) [getter] — a repository's README, decoded to text.
export function getReadme(owner, repo) {
  if (!owner || !repo) return { ok: false, error: "owner and repo are required" };
  var token = _token();
  if (!token) return _notConnected();
  var resp = ghFetch(token, "/repos/" + _enc(owner) + "/" + _enc(repo) + "/readme");
  if (_isErr(resp)) return resp;
  var data = resp.body;
  var text = data.content ? _b64decode(data.content) : "";
  return { ok: true, path: data.path, text: _trim(text, 8000) };
}

// getContents(owner, repo, path) [getter] — a file (decoded) or directory listing.
export function getContents(owner, repo, path) {
  if (!owner || !repo) return { ok: false, error: "owner and repo are required" };
  if (path === undefined || path === null) return { ok: false, error: "path is required" };
  var token = _token();
  if (!token) return _notConnected();
  var url = "/repos/" + _enc(owner) + "/" + _enc(repo) + "/contents/"
    + String(path).split("/").map(_enc).join("/");
  var resp = ghFetch(token, url);
  if (_isErr(resp)) return resp;
  var data = resp.body;
  if (Array.isArray(data)) {
    var entries = [];
    var i;
    for (i = 0; i < data.length; i++) {
      entries.push({ name: data[i].name, path: data[i].path, type: data[i].type });
    }
    return { ok: true, kind: "dir", entries: entries };
  }
  var text = data.content ? _b64decode(data.content) : "";
  return { ok: true, kind: "file", path: data.path, text: _trim(text, 8000) };
}
