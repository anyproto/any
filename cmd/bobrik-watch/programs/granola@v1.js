// __main_source
// __tags: integration
// granola@v1 — read-only connector for Granola (AI meeting notes).
// Pattern-1 token connector: reads cfg.GRANOLA_API_KEY from config@v1 and sends
// it as `Authorization: Bearer grn_...` against Granola's official public REST
// API. Surfaces meeting notes (list), a single note with summary + optional
// transcript, and the folder tree. Read-only — the Granola public API has no
// writes. All methods return a consistent {ok, ...} / {ok:false, error} shape.
//
// IMPORTANT plan gate: Granola API keys are minted only on Business/Enterprise
// plans (Settings → Connectors → API keys). Free/Basic users cannot get a grn_
// key, so this connector cannot reach them via the official path.
//
// CAVEAT — endpoint shapes: the Granola public API is young (~Feb 2026) and
// these paths/field names (GET /v1/notes, GET /v1/notes/{id}?include=transcript,
// GET /v1/folders, cursor pagination) are from the plan's 2026-06 verification.
// CONFIRM them against Granola's live API docs on first use; adjust _BASE / the
// response field reads below if they have drifted.
//
// See dev space → Integration Docs → 06-granola.

import { main as getConfig } from "config@v1";

var BASE = "https://public-api.granola.ai/v1";
var KEY_PATH = "Granola desktop app → Settings → Connectors → API keys";
var DEFAULT_LIMIT = 25;
var MAX_LIMIT = 100;
var MAX_PAGES = 20;           // hard ceiling so a maxItems loop can't run away
var RATE_RETRIES = 3;         // bounded 429 backoff attempts per request

// --- credential ---------------------------------------------------------

// _token() — resolve the grn_ key from config@v1, or throw a clear, actionable
// "not connected" error the agent can relay (and offer to save). Surfaces the
// Business/Enterprise plan gate so the user isn't sent on a fruitless hunt.
function _token() {
  var t = getConfig().GRANOLA_API_KEY;
  if (!t) {
    throw new Error("Granola not connected — create an API key in "
      + KEY_PATH + " (requires a Granola Business or Enterprise plan; free/Basic "
      + "plans cannot mint a key) and add it to config@v1 as GRANOLA_API_KEY "
      + "(or paste the grn_ key to me and I'll save it).");
  }
  return t;
}

// _clampLimit(n) — keep page sizes sane: default small, hard-cap at MAX_LIMIT.
function _clampLimit(n) {
  if (typeof n !== "number" || !(n > 0)) return DEFAULT_LIMIT;
  if (n > MAX_LIMIT) return MAX_LIMIT;
  return Math.floor(n);
}

// --- HTTP transport -----------------------------------------------------

// _qs(params) — build a "?a=b&c=d" query string from a flat object, skipping
// null/undefined/empty values. Values are URI-encoded.
function _qs(params) {
  var parts = [];
  for (var k in params) {
    if (!params.hasOwnProperty(k)) continue;
    var v = params[k];
    if (v === null || v === undefined || v === "") continue;
    parts.push(encodeURIComponent(k) + "=" + encodeURIComponent(String(v)));
  }
  return parts.length ? "?" + parts.join("&") : "";
}

// _get(path) — single GET against BASE+path with the Bearer key. Returns
// { ok, body } on success or { ok:false, error, status } on failure. Honours
// 429 with a bounded Retry-After (or fixed) backoff. 401/403 map back to the
// key + the plan gate.
function _get(path) {
  var key;
  try {
    key = _token();
  } catch (e) {
    return { ok: false, error: e && e.message ? e.message : String(e) };
  }

  var attempt = 0;
  while (true) {
    var resp;
    try {
      resp = fetch(BASE + path, {
        method: "GET",
        headers: {
          "Authorization": "Bearer " + key,
          "Content-Type": "application/json"
        }
      });
    } catch (e) {
      return { ok: false, error: "fetch failed: " + (e && e.message ? e.message : String(e)) };
    }

    if (!resp) return { ok: false, error: "no response from Granola" };

    if (resp.status === 429 && attempt < RATE_RETRIES) {
      attempt++;
      var ra = resp.headers && (resp.headers["retry-after"] || resp.headers["Retry-After"]);
      var waitMs = ra ? (parseInt(ra, 10) * 1000) : (attempt * 1000);
      if (!(waitMs > 0)) waitMs = attempt * 1000;
      sleep(waitMs);
      continue;
    }

    if (!resp.ok) {
      if (resp.status === 401 || resp.status === 403) {
        return {
          ok: false,
          status: resp.status,
          error: "Granola rejected the API key (HTTP " + resp.status + "). "
            + "Check GRANOLA_API_KEY in config@v1, or mint a new key in " + KEY_PATH
            + " (requires a Business/Enterprise plan)."
        };
      }
      var body = resp.body;
      var hmsg = (body && (body.error || body.errors || body.message))
        ? JSON.stringify(body.error || body.errors || body.message)
        : "HTTP " + resp.status;
      return { ok: false, status: resp.status, error: hmsg };
    }

    return { ok: true, body: resp.body };
  }
}

// _nextCursor(body) — pull the pagination cursor from a list response under the
// likely field names. Returns null when there's no next page.
function _nextCursor(body) {
  if (!body) return null;
  if (body.next_cursor) return body.next_cursor;
  if (body.nextCursor) return body.nextCursor;
  if (body.cursor) return body.cursor;
  if (body.pagination && body.pagination.next_cursor) return body.pagination.next_cursor;
  return null;
}

// _rows(body, keys) — extract the array payload from a list response under the
// first matching key (the API wraps rows under e.g. `notes` / `data` / `folders`).
function _rows(body, keys) {
  if (!body) return [];
  for (var i = 0; i < keys.length; i++) {
    if (Array.isArray(body[keys[i]])) return body[keys[i]];
  }
  if (Array.isArray(body)) return body;
  return [];
}

// --- tool methods -------------------------------------------------------

// verify() — connectivity check + key validator. Pulls a 1-item note page and
// reports whether the key works. Use this right after the user saves a key.
export function verify() {
  var r = _get("/notes" + _qs({ limit: 1 }));
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  return { ok: true, connected: true };
}

// listNotes(opts) — page the user's meeting notes (newest-first per the API).
// A note only appears once its AI summary + transcript have finished generating.
// opts: { createdAfter? (ISO-8601), folderId?, cursor?, limit?, maxItems? }.
// When maxItems is set, follows the cursor across pages (bounded by MAX_PAGES)
// and returns up to maxItems rows; otherwise returns one page + nextCursor.
export function listNotes(opts) {
  opts = opts || {};
  var limit = _clampLimit(opts.limit);
  var cursor = opts.cursor || null;
  var maxItems = (typeof opts.maxItems === "number" && opts.maxItems > 0) ? Math.floor(opts.maxItems) : null;

  var notes = [];
  var pages = 0;
  var lastCursor = cursor;

  while (true) {
    var r = _get("/notes" + _qs({
      created_after: opts.createdAfter,
      folder_id: opts.folderId,
      cursor: lastCursor,
      limit: limit
    }));
    if (!r.ok) return { ok: false, error: r.error, status: r.status };

    var rows = _rows(r.body, ["notes", "data"]);
    for (var i = 0; i < rows.length; i++) notes.push(rows[i]);
    pages++;
    lastCursor = _nextCursor(r.body);

    if (maxItems === null) break;                 // single-page mode
    if (notes.length >= maxItems) { notes = notes.slice(0, maxItems); lastCursor = lastCursor; break; }
    if (!lastCursor) break;
    if (pages >= MAX_PAGES) break;
  }

  return { ok: true, notes: notes, nextCursor: maxItems === null ? lastCursor : (lastCursor || null) };
}

// getNote(opts) — one note with summary and (optionally) the raw transcript.
// opts: { id (required), includeTranscript? }. Accepts a bare id string too.
// A freshly-ended meeting whose summary/transcript hasn't generated yet may
// 404 — retry shortly.
export function getNote(opts) {
  if (typeof opts === "string") opts = { id: opts };
  opts = opts || {};
  if (!opts.id || typeof opts.id !== "string") return { ok: false, error: "id is required" };
  var path = "/notes/" + encodeURIComponent(opts.id);
  if (opts.includeTranscript) path += _qs({ include: "transcript" });
  var r = _get(path);
  if (!r.ok) {
    if (r.status === 404) {
      return { ok: false, status: 404,
        error: "note not found (or its summary/transcript hasn't generated yet): " + opts.id };
    }
    return { ok: false, error: r.error, status: r.status };
  }
  var note = (r.body && r.body.note) ? r.body.note : r.body;
  if (!note) return { ok: false, error: "empty note response for: " + opts.id };
  return { ok: true, note: note };
}

// listFolders(opts) — accessible folders (hierarchy via parent_folder_id),
// cursor-paginated. opts: { cursor?, limit?, maxItems? }. Same paging contract
// as listNotes.
export function listFolders(opts) {
  opts = opts || {};
  var limit = _clampLimit(opts.limit);
  var cursor = opts.cursor || null;
  var maxItems = (typeof opts.maxItems === "number" && opts.maxItems > 0) ? Math.floor(opts.maxItems) : null;

  var folders = [];
  var pages = 0;
  var lastCursor = cursor;

  while (true) {
    var r = _get("/folders" + _qs({ cursor: lastCursor, limit: limit }));
    if (!r.ok) return { ok: false, error: r.error, status: r.status };

    var rows = _rows(r.body, ["folders", "data"]);
    for (var i = 0; i < rows.length; i++) folders.push(rows[i]);
    pages++;
    lastCursor = _nextCursor(r.body);

    if (maxItems === null) break;
    if (folders.length >= maxItems) { folders = folders.slice(0, maxItems); break; }
    if (!lastCursor) break;
    if (pages >= MAX_PAGES) break;
  }

  return { ok: true, folders: folders, nextCursor: maxItems === null ? lastCursor : (lastCursor || null) };
}

// main(args) — default entry point: a quick connectivity check (verify).
export function main(args) {
  return verify();
}
