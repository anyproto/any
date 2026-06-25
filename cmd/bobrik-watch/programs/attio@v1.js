// __main_source
// __tags: integration
// attio@v1 — read-only Attio CRM connector. Pattern-1 token connector; reads
// cfg.ATTIO_API_TOKEN from config@v1. Surfaces people / companies / deals /
// lists / notes / workspace members from the user's Attio workspace into the
// second brain. Ingestion only — never writes back to Attio.
//
// GOTCHA: Attio access tokens default to NO scopes and silently 403 until the
// user grants read scopes on the integration (Workspace settings → Developers).
// The "not connected" / 403 errors below enumerate the required read scopes.

import { main as getConfig } from "config@v1";

var BASE = "https://api.attio.com/v2";

// The read scopes a freshly-created Attio token needs. Surfaced verbatim in the
// not-connected / 403 errors so the user can fix the opaque permission failure.
var REQUIRED_SCOPES =
  "record_permission:read, object_configuration:read, list_entry:read, " +
  "user_management:read, note:read";

var SCOPE_HINT =
  "Attio tokens default to NO scopes — create/edit the integration under " +
  "Workspace settings → Developers and grant these read scopes: " +
  REQUIRED_SCOPES + ".";

function _token() {
  var cfg = getConfig();
  var token = cfg && cfg.ATTIO_API_TOKEN;
  if (!token) {
    return {
      ok: false,
      error: "Attio not connected — create an access token at Workspace " +
        "settings → Developers (developers.attio.com), grant the read scopes, " +
        "and add it to config@v1 as ATTIO_API_TOKEN (or paste it to me and " +
        "I'll save it). " + SCOPE_HINT
    };
  }
  return { ok: true, token: token };
}

function _headers(token) {
  return {
    "Authorization": "Bearer " + token,
    "Content-Type": "application/json"
  };
}

// _err — normalize a non-ok fetch response into a consistent {ok:false} shape.
// A 403 almost always means missing scopes, so spell that out.
function _err(resp) {
  var status = resp ? resp.status : "?";
  var detail = "";
  if (resp && resp.body) {
    if (resp.body.message) detail = resp.body.message;
    else if (resp.body.errors) detail = JSON.stringify(resp.body.errors);
    else if (resp.body.error) detail = JSON.stringify(resp.body.error);
  }
  if (status === 403) {
    return {
      ok: false,
      status: 403,
      error: "Attio returned 403 (forbidden) — almost always missing token " +
        "scopes. " + SCOPE_HINT + (detail ? " (" + detail + ")" : "")
    };
  }
  if (status === 401) {
    return {
      ok: false,
      status: 401,
      error: "Attio returned 401 — the ATTIO_API_TOKEN is missing or invalid. " +
        "Create a fresh token at Workspace settings → Developers." +
        (detail ? " (" + detail + ")" : "")
    };
  }
  return {
    ok: false,
    status: status,
    error: "Attio HTTP " + status + (detail ? ": " + detail : "")
  };
}

// _request — single HTTP call with bounded 429 backoff. Returns the parsed
// body on success or a {ok:false,error} object on failure.
function _request(method, path, body) {
  var tok = _token();
  if (!tok.ok) return tok;

  var url = BASE + path;
  var opts = { method: method, headers: _headers(tok.token) };
  if (body) opts.body = JSON.stringify(body);

  var attempts = 0;
  var resp;
  while (attempts < 4) {
    resp = fetch(url, opts);
    if (resp && resp.status === 429) {
      // Honor Retry-After (seconds) if present; else exponential backoff.
      var ra = resp.headers &&
        (resp.headers["retry-after"] || resp.headers["Retry-After"]);
      var waitMs = ra ? (parseInt(ra, 10) * 1000) : (500 * (attempts + 1));
      if (!waitMs || waitMs < 0) waitMs = 500;
      sleep(waitMs);
      attempts++;
      continue;
    }
    break;
  }

  if (!resp || !resp.ok) return _err(resp);
  return { ok: true, body: resp.body };
}

function _get(path) { return _request("GET", path, null); }
function _post(path, body) { return _request("POST", path, body); }

// _clampLimit — Attio record/list-entry queries cap limit at 500 (default 500).
// Keep agent-facing defaults small to protect context; allow up to 500.
function _clampLimit(limit, dflt) {
  var n = (typeof limit === "number" && limit > 0) ? limit : dflt;
  if (n > 500) n = 500;
  return n;
}

// --- tool methods (each maps 1:1 to a ## Tool Schema entry) ---

// whoami — credential/scope sanity check. There is no dedicated "self"
// endpoint for a token, so confirm the token + scopes work by listing
// workspace members and surface a friendly error early.
export function whoami() {
  var res = _get("/workspace_members");
  if (!res.ok) return res;
  var members = (res.body && res.body.data) || [];
  return { ok: true, connected: true, memberCount: members.length, members: members };
}

// listObjects — GET /v2/objects. Discovers which standard/custom objects
// exist and their slugs (people / companies / deals / …).
export function listObjects() {
  var res = _get("/objects");
  if (!res.ok) return res;
  return { ok: true, objects: (res.body && res.body.data) || [] };
}

// listAttributes(object) — GET /v2/objects/{object}/attributes. The schema
// for one object; needed to know which attribute slugs to read/map.
export function listAttributes(object) {
  if (!object) return { ok: false, error: "object (slug or UUID) is required" };
  var res = _get("/objects/" + encodeURIComponent(object) + "/attributes");
  if (!res.ok) return res;
  return { ok: true, attributes: (res.body && res.body.data) || [] };
}

// queryRecords(object, opts) — POST /v2/objects/{object}/records/query. The
// workhorse: pages people / companies / deals with optional filter + sort.
// opts: { filter?, sorts?, limit? (default 100, max 500), offset? (default 0) }.
export function queryRecords(object, opts) {
  if (!object) return { ok: false, error: "object (slug or UUID) is required" };
  opts = opts || {};
  var body = {
    limit: _clampLimit(opts.limit, 100),
    offset: (typeof opts.offset === "number" && opts.offset >= 0) ? opts.offset : 0
  };
  if (opts.filter) body.filter = opts.filter;
  if (opts.sorts) body.sorts = opts.sorts;
  var res = _post("/objects/" + encodeURIComponent(object) + "/records/query", body);
  if (!res.ok) return res;
  var records = (res.body && res.body.data) || [];
  return { ok: true, records: records, count: records.length, offset: body.offset, limit: body.limit };
}

// getRecord(object, recordId) — GET /v2/objects/{object}/records/{record_id}.
// Single-record fetch by record_id UUID.
export function getRecord(object, recordId) {
  if (!object) return { ok: false, error: "object (slug or UUID) is required" };
  if (!recordId) return { ok: false, error: "recordId (UUID) is required" };
  var res = _get("/objects/" + encodeURIComponent(object) +
    "/records/" + encodeURIComponent(recordId));
  if (!res.ok) return res;
  return { ok: true, record: (res.body && res.body.data) || null };
}

// listLists — GET /v2/lists. Lists the user's pipelines/segments.
export function listLists() {
  var res = _get("/lists");
  if (!res.ok) return res;
  return { ok: true, lists: (res.body && res.body.data) || [] };
}

// queryListEntries(list, opts) — POST /v2/lists/{list}/entries/query. Pages a
// list's entries. Same opts semantics as queryRecords.
export function queryListEntries(list, opts) {
  if (!list) return { ok: false, error: "list (slug or UUID) is required" };
  opts = opts || {};
  var body = {
    limit: _clampLimit(opts.limit, 100),
    offset: (typeof opts.offset === "number" && opts.offset >= 0) ? opts.offset : 0
  };
  if (opts.filter) body.filter = opts.filter;
  if (opts.sorts) body.sorts = opts.sorts;
  var res = _post("/lists/" + encodeURIComponent(list) + "/entries/query", body);
  if (!res.ok) return res;
  var entries = (res.body && res.body.data) || [];
  return { ok: true, entries: entries, count: entries.length, offset: body.offset, limit: body.limit };
}

// listNotes(opts) — GET /v2/notes. Free-text notes attached to records.
// opts: { limit? (default 50), offset? (default 0) }.
export function listNotes(opts) {
  opts = opts || {};
  var limit = _clampLimit(opts.limit, 50);
  var offset = (typeof opts.offset === "number" && opts.offset >= 0) ? opts.offset : 0;
  var res = _get("/notes?limit=" + limit + "&offset=" + offset);
  if (!res.ok) return res;
  var notes = (res.body && res.body.data) || [];
  return { ok: true, notes: notes, count: notes.length, offset: offset, limit: limit };
}

// listWorkspaceMembers — GET /v2/workspace_members. Team roster for
// owner/assignee resolution.
export function listWorkspaceMembers() {
  var res = _get("/workspace_members");
  if (!res.ok) return res;
  return { ok: true, members: (res.body && res.body.data) || [] };
}

export function main() {
  return whoami();
}
