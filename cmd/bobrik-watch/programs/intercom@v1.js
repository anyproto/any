// __main_source
// __tags: integration
// intercom@v1 — read a connected Intercom workspace into the second brain.
// Pattern-1 token connector: reads cfg.INTERCOM_ACCESS_TOKEN from config@v1.
// Read-first / ingestion-only — surfaces customer conversations (with full
// transcripts), contacts/leads, and help-center articles. Every call sends
// `Authorization: Bearer <token>`, `Accept: application/json`, and the REQUIRED
// `Intercom-Version: 2.15` pin. Base URL https://api.intercom.io (override for
// EU/AU workspaces). Cursor pagination via pages.next.starting_after; 429 /
// Retry-After backoff. See dev space → Integration Docs → 11.

import { main as getConfig } from "config@v1";

var BASE = "https://api.intercom.io";
var API_VERSION = "2.15";
var MAX_RETRIES = 3;

function _token() {
  var t = getConfig().INTERCOM_ACCESS_TOKEN;
  if (!t) {
    throw new Error("Intercom not connected — create an Access Token in the "
      + "Developer Hub (Settings → Developers → your app → Configure → "
      + "Authentication) and add it to config@v1 as INTERCOM_ACCESS_TOKEN "
      + "(or paste it to me and I'll save it).");
  }
  return t;
}

function _headers() {
  return {
    "Authorization": "Bearer " + _token(),
    "Accept": "application/json",
    "Content-Type": "application/json",
    "Intercom-Version": API_VERSION
  };
}

// _qs(params) — build a URL query string from a flat object, skipping
// undefined/null. Values are coerced to strings and encoded.
function _qs(params) {
  if (!params) return "";
  var parts = [];
  for (var k in params) {
    if (!params.hasOwnProperty(k)) continue;
    var v = params[k];
    if (v === undefined || v === null || v === "") continue;
    parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(String(v)));
  }
  return parts.length ? ("?" + parts.join("&")) : "";
}

// _retryAfterMs(resp) — read the Retry-After header (seconds) into ms, with a
// sane default when the header is absent on a 429.
function _retryAfterMs(resp) {
  if (resp && resp.headers) {
    var ra = resp.headers["retry-after"] || resp.headers["Retry-After"];
    var secs = parseInt(ra, 10);
    if (!isNaN(secs) && secs > 0) return secs * 1000;
  }
  return 2000;
}

// _req(method, path, opts) — single HTTP call with the three required headers,
// query-string for GET params, JSON body for POST, and bounded 429/Retry-After
// backoff. Returns the parsed body or throws with a clean message.
function _req(method, path, opts) {
  opts = opts || {};
  var url = BASE + path + _qs(opts.params);
  var fetchOpts = { method: method, headers: _headers() };
  if (opts.body !== undefined) fetchOpts.body = JSON.stringify(opts.body);

  var attempt = 0;
  var resp;
  while (true) {
    resp = fetch(url, fetchOpts);
    if (resp && resp.status === 429 && attempt < MAX_RETRIES) {
      sleep(_retryAfterMs(resp));
      attempt++;
      continue;
    }
    break;
  }

  if (!resp || !resp.ok) {
    var body = resp && resp.body;
    var msg;
    if (body && body.errors) msg = JSON.stringify(body.errors);
    else if (body && body.error) msg = JSON.stringify(body.error);
    else msg = "HTTP " + (resp ? resp.status : "?");
    var err = new Error("Intercom " + method + " " + path + " failed: " + msg);
    err.status = resp && resp.status;
    throw err;
  }
  return resp.body;
}

// _ok(extra) — wrap a successful payload in the consistent {ok:true, ...} shape.
function _ok(extra) {
  var out = { ok: true };
  for (var k in extra) {
    if (extra.hasOwnProperty(k)) out[k] = extra[k];
  }
  return out;
}

// _fail(e) — normalize any thrown error into {ok:false, error, status?}.
function _fail(e) {
  var out = { ok: false, error: (e && e.message) ? e.message : String(e) };
  if (e && e.status !== undefined) out.status = e.status;
  return out;
}

// _clampPerPage(n) — Intercom per_page max is 150; default 20.
function _clampPerPage(n) {
  if (n === undefined || n === null) return undefined;
  var v = parseInt(n, 10);
  if (isNaN(v) || v <= 0) return undefined;
  return v > 150 ? 150 : v;
}

// --- tool methods (each maps 1:1 to a ## Tool Schema entry) ---

// me() — the authenticated app/admin context; the cheapest connectivity check.
export function me() {
  try {
    var data = _req("GET", "/me", {});
    return _ok({ me: data });
  } catch (e) {
    return _fail(e);
  }
}

// listConversations(opts) — page conversation SUMMARIES (no parts), newest
// updated first. opts: {open?, sort?, order?, perPage?, startingAfter?}.
export function listConversations(opts) {
  opts = opts || {};
  try {
    var params = {
      per_page: _clampPerPage(opts.perPage),
      starting_after: opts.startingAfter,
      sort: opts.sort,
      order: opts.order
    };
    if (opts.open !== undefined) params.open = opts.open ? "true" : "false";
    var data = _req("GET", "/conversations", { params: params });
    return _ok({ conversations: data.conversations || [], pages: data.pages || null });
  } catch (e) {
    return _fail(e);
  }
}

// searchConversations(query, opts) — filtered conversation search via the
// Intercom query DSL ({field, operator, value}, AND/OR groups). Cursor goes in
// the pagination object. opts: {perPage?, startingAfter?}.
export function searchConversations(query, opts) {
  opts = opts || {};
  try {
    if (!query) return { ok: false, error: "query is required (Intercom search DSL: {field, operator, value})" };
    var pagination = {};
    var pp = _clampPerPage(opts.perPage);
    if (pp !== undefined) pagination.per_page = pp;
    if (opts.startingAfter) pagination.starting_after = opts.startingAfter;
    var body = { query: query };
    if (pagination.per_page !== undefined || pagination.starting_after !== undefined) {
      body.pagination = pagination;
    }
    var data = _req("POST", "/conversations/search", { body: body });
    return _ok({ conversations: data.conversations || [], pages: data.pages || null });
  } catch (e) {
    return _fail(e);
  }
}

// getConversation(id, opts) — one conversation WITH its message parts
// (transcript). opts: {plaintext?} (default true → display_as=plaintext bodies).
// Capped at the 500 most recent parts by Intercom.
export function getConversation(id, opts) {
  opts = opts || {};
  try {
    if (!id) return { ok: false, error: "conversation id is required" };
    var plaintext = opts.plaintext === undefined ? true : !!opts.plaintext;
    var params = {};
    if (plaintext) params.display_as = "plaintext";
    var data = _req("GET", "/conversations/" + encodeURIComponent(id), { params: params });
    return _ok({ conversation: data });
  } catch (e) {
    return _fail(e);
  }
}

// listContacts(opts) — page contacts and leads. opts: {perPage?, startingAfter?}.
export function listContacts(opts) {
  opts = opts || {};
  try {
    var params = {
      per_page: _clampPerPage(opts.perPage),
      starting_after: opts.startingAfter
    };
    var data = _req("GET", "/contacts", { params: params });
    return _ok({ data: data.data || [], pages: data.pages || null });
  } catch (e) {
    return _fail(e);
  }
}

// searchContacts(query, opts) — filtered contact search (e.g. by email or
// custom attribute) via the Intercom query DSL. opts: {perPage?, startingAfter?}.
export function searchContacts(query, opts) {
  opts = opts || {};
  try {
    if (!query) return { ok: false, error: "query is required (Intercom search DSL: {field, operator, value})" };
    var pagination = {};
    var pp = _clampPerPage(opts.perPage);
    if (pp !== undefined) pagination.per_page = pp;
    if (opts.startingAfter) pagination.starting_after = opts.startingAfter;
    var body = { query: query };
    if (pagination.per_page !== undefined || pagination.starting_after !== undefined) {
      body.pagination = pagination;
    }
    var data = _req("POST", "/contacts/search", { body: body });
    return _ok({ data: data.data || [], pages: data.pages || null });
  } catch (e) {
    return _fail(e);
  }
}

// listArticles(opts) — page help-center articles. opts: {perPage?, startingAfter?}.
export function listArticles(opts) {
  opts = opts || {};
  try {
    var params = {
      per_page: _clampPerPage(opts.perPage),
      starting_after: opts.startingAfter
    };
    var data = _req("GET", "/articles", { params: params });
    return _ok({ data: data.data || [], pages: data.pages || null });
  } catch (e) {
    return _fail(e);
  }
}

export function main() {
  return me();
}
