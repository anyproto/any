// __main_source
// __tags: integration
// gmail@v1 — read-only connector for Gmail (REST v1). OAuth, NOT a Pattern-1
// token: it never reads a key from config@v1 and never runs the consent flow
// itself. It imports the shared googleAuth@v1 layer (one Google client / one
// consent / one refresh token covers gmail + sheets + calendar + drive) and
// makes authed GET reads through getJSON() — access-token refresh is handled
// inside the helper. If a call surfaces a not-connected error, tell the user to
// run `googleAuth.connect()` once and save the refresh token to config@v1.
//
// Surfaces recent message stubs, single messages (decoded headers + text/plain
// body), threads, labels, and a thin q: search wrapper. Returned message
// objects are TRIMMED (headers + snippet + decoded text) — never the raw
// payload MIME tree — so they don't flood agent context. All methods return a
// consistent {ok, ...} / {ok:false, error} shape. Scope: gmail.readonly (a
// SENSITIVE scope — the OAuth app needs Google verification beyond test users).
// See dev space → Integration Docs → 01-gmail.
import { getJSON } from "googleAuth@v1";

var BASE = "https://gmail.googleapis.com/gmail/v1/users/me";
var DEFAULT_MAX = 20;
var HARD_MAX = 100;

// --- helpers ------------------------------------------------------------

// _max(n) — keep page sizes sane: default small, hard-cap at HARD_MAX. Gmail
// allows up to 500, but messages.get costs 20 quota units each, so a big page
// fanned out into getMessage calls burns quota fast — cap conservatively.
function _max(n) {
  if (typeof n !== "number" || !(n > 0)) return DEFAULT_MAX;
  if (n > HARD_MAX) return HARD_MAX;
  return Math.floor(n);
}

// _qs(params) — build a query string from a {key: value|array} map, skipping
// null/undefined/empty. Array values repeat the key (labelIds[]). URL-encoded.
function _qs(params) {
  var parts = [];
  for (var k in params) {
    if (!Object.prototype.hasOwnProperty.call(params, k)) continue;
    var v = params[k];
    if (v === null || v === undefined || v === "") continue;
    if (Array.isArray(v)) {
      for (var i = 0; i < v.length; i++) {
        if (v[i] === null || v[i] === undefined || v[i] === "") continue;
        parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(v[i]));
      }
    } else {
      parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(v));
    }
  }
  return parts.length ? "?" + parts.join("&") : "";
}

// _surface(r) — normalize a getJSON {ok:false,error,status} into a clean error,
// upgrading the not-connected case into an actionable hint pointing the user at
// googleAuth.connect(). The helper throws cleanly on missing config/refresh
// token; those surface here as the error string.
function _surface(r) {
  var msg = (r && r.error) ? String(r.error) : "Gmail request failed";
  var low = msg.toLowerCase();
  if (low.indexOf("not connected") !== -1 || low.indexOf("not configured") !== -1
      || low.indexOf("refresh") !== -1 || low.indexOf("connect()") !== -1) {
    return {
      ok: false,
      status: r && r.status,
      error: msg + " — run googleAuth.connect() once (then save the returned "
        + "refresh token to config@v1 as GOOGLE_OAUTH_REFRESH_TOKEN)."
    };
  }
  return { ok: false, status: r && r.status, error: msg };
}

// _get(path) — authed GET via the shared googleAuth layer. getJSON handles the
// Bearer token + transparent refresh; we only translate its error shape. Wrapped
// in try/catch because the helper throws (clean Error) when Google is
// unconfigured or the refresh token is absent.
function _get(path) {
  var r;
  try {
    r = getJSON(BASE + path);
  } catch (e) {
    return _surface({ error: e && e.message ? e.message : String(e) });
  }
  if (!r || !r.ok) return _surface(r);
  return { ok: true, body: r.body };
}

// _b64urlDecode(s) — decode a base64url string (Gmail uses URL-safe base64, no
// padding) into a UTF-8 string. `atob` is available in the Sobek runtime and
// yields a binary string; we re-decode it as UTF-8 by hand (atob gives one
// char per byte). Returns "" on any failure rather than throwing.
function _b64urlDecode(s) {
  if (!s || typeof s !== "string") return "";
  var b64 = s.replace(/-/g, "+").replace(/_/g, "/");
  while (b64.length % 4 !== 0) b64 += "=";
  var bin;
  try {
    bin = atob(b64);
  } catch (e) {
    return "";
  }
  // Re-interpret the binary string as UTF-8 bytes.
  var bytes = [];
  for (var i = 0; i < bin.length; i++) bytes.push(bin.charCodeAt(i) & 0xff);
  try {
    return _utf8Decode(bytes);
  } catch (e2) {
    return bin;
  }
}

// _utf8Decode(bytes) — minimal UTF-8 byte-array → string decoder (no
// TextDecoder dependency). Handles 1–4 byte sequences; skips malformed bytes.
function _utf8Decode(bytes) {
  var out = "";
  var i = 0;
  while (i < bytes.length) {
    var c = bytes[i++];
    if (c < 0x80) {
      out += String.fromCharCode(c);
    } else if (c >= 0xc0 && c < 0xe0) {
      var c2 = bytes[i++] & 0x3f;
      out += String.fromCharCode(((c & 0x1f) << 6) | c2);
    } else if (c >= 0xe0 && c < 0xf0) {
      var d2 = bytes[i++] & 0x3f;
      var d3 = bytes[i++] & 0x3f;
      out += String.fromCharCode(((c & 0x0f) << 12) | (d2 << 6) | d3);
    } else if (c >= 0xf0) {
      var e2 = bytes[i++] & 0x3f;
      var e3 = bytes[i++] & 0x3f;
      var e4 = bytes[i++] & 0x3f;
      var cp = ((c & 0x07) << 18) | (e2 << 12) | (e3 << 6) | e4;
      cp -= 0x10000;
      out += String.fromCharCode(0xd800 + (cp >> 10), 0xdc00 + (cp & 0x3ff));
    }
  }
  return out;
}

// _headers(payload) — pull the interesting headers out of a payload's
// headers[] into a {from,to,cc,subject,date} map (case-insensitive names).
function _headers(payload) {
  var out = { from: "", to: "", cc: "", subject: "", date: "" };
  var hs = payload && payload.headers;
  if (!hs) return out;
  for (var i = 0; i < hs.length; i++) {
    var h = hs[i];
    if (!h || !h.name) continue;
    var n = h.name.toLowerCase();
    if (n === "from") out.from = h.value || "";
    else if (n === "to") out.to = h.value || "";
    else if (n === "cc") out.cc = h.value || "";
    else if (n === "subject") out.subject = h.value || "";
    else if (n === "date") out.date = h.value || "";
  }
  return out;
}

// _extractText(payload) — walk the MIME tree and return the first text/plain
// body, falling back to a stripped text/html body if no plain part exists.
// Gmail nests parts arbitrarily (multipart/alternative inside multipart/mixed),
// so this recurses. Body data is base64url.
function _extractText(payload) {
  var plain = _findPart(payload, "text/plain");
  if (plain) return plain;
  var html = _findPart(payload, "text/html");
  if (html) return _stripHtml(html);
  return "";
}

// _findPart(payload, mime) — depth-first search for the first part whose
// mimeType matches `mime` and carries body data; returns the DECODED text or "".
function _findPart(payload, mime) {
  if (!payload) return "";
  if (payload.mimeType === mime && payload.body && payload.body.data) {
    return _b64urlDecode(payload.body.data);
  }
  var parts = payload.parts;
  if (parts) {
    for (var i = 0; i < parts.length; i++) {
      var got = _findPart(parts[i], mime);
      if (got) return got;
    }
  }
  return "";
}

// _stripHtml(s) — crude HTML → text fallback for html-only mail: drop tags,
// collapse whitespace, decode a few common entities. Lossy by design (v1).
function _stripHtml(s) {
  if (!s) return "";
  var t = s.replace(/<\s*(script|style)[^>]*>[\s\S]*?<\s*\/\s*\1\s*>/gi, " ");
  t = t.replace(/<[^>]+>/g, " ");
  t = t.replace(/&nbsp;/gi, " ").replace(/&amp;/gi, "&").replace(/&lt;/gi, "<")
       .replace(/&gt;/gi, ">").replace(/&quot;/gi, "\"").replace(/&#39;/gi, "'");
  t = t.replace(/[ \t]+/g, " ").replace(/\s*\n\s*/g, "\n");
  return t.replace(/\n{3,}/g, "\n\n").trim();
}

// _trimMessage(raw) — map a raw messages.get response into the trimmed shape we
// return to the agent: ids + labels + decoded headers + snippet + body text.
// The raw payload tree is intentionally dropped.
function _trimMessage(raw) {
  if (!raw) return null;
  var h = _headers(raw.payload);
  return {
    id: raw.id,
    threadId: raw.threadId,
    labelIds: raw.labelIds || [],
    historyId: raw.historyId,
    internalDate: raw.internalDate,
    from: h.from,
    to: h.to,
    cc: h.cc,
    subject: h.subject,
    date: h.date,
    snippet: raw.snippet || "",
    body: _extractText(raw.payload)
  };
}

// --- tool methods -------------------------------------------------------

// listMessages(opts) — list message stubs ({id, threadId}) matching a Gmail
// search query / labels. Thin wrap of GET /messages. opts:
// { q?, labelIds?, maxResults?, pageToken? }.
export function listMessages(opts) {
  opts = opts || {};
  var path = "/messages" + _qs({
    q: opts.q,
    labelIds: opts.labelIds,
    maxResults: _max(opts.maxResults),
    pageToken: opts.pageToken
  });
  var r = _get(path);
  if (!r.ok) return r;
  var b = r.body || {};
  return {
    ok: true,
    messages: b.messages || [],
    nextPageToken: b.nextPageToken || null,
    resultSizeEstimate: b.resultSizeEstimate
  };
}

// search(query, opts) — convenience wrapper over listMessages with the Gmail
// `q:` search syntax (e.g. "from:boss newer_than:7d is:unread"). opts:
// { labelIds?, maxResults?, pageToken? }.
export function search(query, opts) {
  if (!query || typeof query !== "string") {
    return { ok: false, error: "query is required (Gmail q: syntax, e.g. \"newer_than:7d from:boss\")" };
  }
  opts = opts || {};
  return listMessages({
    q: query,
    labelIds: opts.labelIds,
    maxResults: opts.maxResults,
    pageToken: opts.pageToken
  });
}

// getMessage(id, opts) — fetch and DECODE one message into the trimmed shape
// (headers + snippet + decoded text/plain body). opts.format ∈
// "full" (default) | "metadata" | "minimal". `metadata` returns headers + snippet
// with no body; `minimal` returns ids/labels only.
export function getMessage(id, opts) {
  if (!id || typeof id !== "string") return { ok: false, error: "id is required" };
  opts = opts || {};
  var format = opts.format || "full";
  var r = _get("/messages/" + encodeURIComponent(id) + _qs({ format: format }));
  if (!r.ok) return r;
  var msg = _trimMessage(r.body);
  if (!msg) return { ok: false, error: "message not found: " + id };
  return { ok: true, message: msg };
}

// getThread(id) — fetch every message in a thread (GET /threads/{id},
// format=full) and return them in the trimmed message shape, in order.
export function getThread(id) {
  if (!id || typeof id !== "string") return { ok: false, error: "id is required" };
  var r = _get("/threads/" + encodeURIComponent(id) + _qs({ format: "full" }));
  if (!r.ok) return r;
  var b = r.body || {};
  var msgs = [];
  var raw = b.messages || [];
  for (var i = 0; i < raw.length; i++) {
    var m = _trimMessage(raw[i]);
    if (m) msgs.push(m);
  }
  return { ok: true, id: b.id || id, historyId: b.historyId, messages: msgs };
}

// listLabels() — the account's labels (GET /labels). No pagination — Gmail
// returns the full fixed set in one call. Each: { id, name, type }.
export function listLabels() {
  var r = _get("/labels");
  if (!r.ok) return r;
  var raw = (r.body && r.body.labels) || [];
  var labels = [];
  for (var i = 0; i < raw.length; i++) {
    labels.push({ id: raw[i].id, name: raw[i].name, type: raw[i].type });
  }
  return { ok: true, labels: labels };
}

// main(args) — default entry point: a quick connectivity check (list labels,
// which is cheap and validates the OAuth connection).
export function main(args) {
  return listLabels();
}
