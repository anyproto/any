// __main_source
// __tags: integration
// figma@v1 — read-only connector for Figma (REST). Pattern-1 token connector:
// reads cfg.FIGMA_TOKEN from config@v1 and sends it in the Figma-specific
// `X-Figma-Token` header (NOT Authorization/Bearer — that's OAuth-only).
// Surfaces the SECOND-BRAIN-useful slices of a design file: account identity,
// lightweight file metadata, threaded comments, and the extracted TEXT-layer
// content of a file — deliberately NOT the multi-megabyte raw node tree.
// Figma has no "list all my files" API, so callers supply a file URL/key.
// Rate limits are tight + per-minute; calls are serialized and 429 honors
// `Retry-After`. All methods return a consistent {ok, ...} / {ok:false, error}.
// See dev space → Integration Docs → 09-figma.

import { main as getConfig } from "config@v1";

var BASE = "https://api.figma.com/v1";
var TOKEN_URL = "https://www.figma.com/settings (Security → Personal access tokens)";
var DEFAULT_DEPTH = 8;
var MAX_RETRIES = 3;

// --- credential ---------------------------------------------------------

// _token() — resolve the PAT from config@v1, or throw a clear, actionable
// "not connected" error the agent can relay (and offer to save).
function _token() {
  var t = getConfig().FIGMA_TOKEN;
  if (!t) {
    throw new Error("Figma not connected — create a Personal Access Token at "
      + TOKEN_URL + ", ticking read scopes (current_user:read, "
      + "file_metadata:read, file_content:read, file_comments:read), then add "
      + "it to config@v1 as FIGMA_TOKEN (or paste it to me and I'll save it).");
  }
  return t;
}

// --- file-key helper ----------------------------------------------------

// _parseFileKey(s) — accept a raw file key OR a pasted figma.com URL of the
// form https://www.figma.com/(file|design|board)/:key/:name and return :key.
// Returns null for empty/non-string input.
function _parseFileKey(s) {
  if (!s || typeof s !== "string") return null;
  var m = s.match(/figma\.com\/(?:file|design|board|proto)\/([A-Za-z0-9]+)/);
  if (m && m[1]) return m[1];
  return s.trim();
}

// --- HTTP transport -----------------------------------------------------

// _get(path) — single serialized GET against the Figma REST API. Sends the
// X-Figma-Token header. On 429 it honors the `Retry-After` header (seconds),
// sleeps, and retries up to MAX_RETRIES times. Returns { ok, body } on success
// or { ok:false, error, status } on failure. 403 = missing scope (re-prompt),
// 404 = bad/inaccessible file key, 401 = bad token.
function _get(path) {
  var token;
  try {
    token = _token();
  } catch (e) {
    return { ok: false, error: e && e.message ? e.message : String(e) };
  }

  var attempt = 0;
  while (true) {
    var resp;
    try {
      resp = fetch(BASE + path, {
        method: "GET",
        headers: { "X-Figma-Token": token }
      });
    } catch (e) {
      return { ok: false, error: "fetch failed: " + (e && e.message ? e.message : String(e)) };
    }

    if (!resp) return { ok: false, error: "no response from Figma" };

    if (resp.status === 429 && attempt < MAX_RETRIES) {
      var retryAfter = resp.headers
        && (resp.headers["retry-after"] || resp.headers["Retry-After"]);
      var waitMs = retryAfter ? (parseInt(retryAfter, 10) * 1000) : (1000 * Math.pow(2, attempt));
      if (!(waitMs > 0)) waitMs = 1000;
      sleep(waitMs);
      attempt = attempt + 1;
      continue;
    }

    if (!resp.ok) {
      var body = resp.body;
      if (resp.status === 401) {
        return { ok: false, status: 401, error: "Figma rejected the token (HTTP 401). "
          + "Check FIGMA_TOKEN in config@v1 — regenerate at " + TOKEN_URL + " if needed." };
      }
      if (resp.status === 403) {
        return { ok: false, status: 403, error: "Figma denied access (HTTP 403) — the token "
          + "is likely missing a required read scope. Regenerate the PAT at " + TOKEN_URL
          + " with current_user:read, file_metadata:read, file_content:read, file_comments:read." };
      }
      if (resp.status === 404) {
        return { ok: false, status: 404, error: "Figma file/resource not found or inaccessible (HTTP 404) — check the file key/URL." };
      }
      if (resp.status === 429) {
        return { ok: false, status: 429, error: "Figma rate limit hit (HTTP 429) — backed off " + MAX_RETRIES + "x and gave up. Try again in a minute." };
      }
      var emsg = (body && body.err) ? body.err
        : ((body && body.message) ? body.message : ("HTTP " + resp.status));
      return { ok: false, status: resp.status, error: "Figma: " + emsg };
    }

    return { ok: true, body: resp.body };
  }
}

// --- text extraction ----------------------------------------------------

// _walkText(node, page, out) — DFS the node tree, collecting every TEXT layer's
// `characters`. `page` is the enclosing CANVAS (page) name. Pushes
// { nodeId, page, name, text } per TEXT node. The walk is bounded by the
// depth-limited tree the API already returned (we pass ?depth=N), so it never
// descends deeper than the server sent.
function _walkText(node, page, out) {
  if (!node || typeof node !== "object") return;
  if (node.type === "CANVAS") page = node.name || page;
  if (node.type === "TEXT" && typeof node.characters === "string" && node.characters.length > 0) {
    out.push({ nodeId: node.id, page: page || "", name: node.name || "", text: node.characters });
  }
  var kids = node.children;
  if (kids && kids.length) {
    for (var i = 0; i < kids.length; i++) _walkText(kids[i], page, out);
  }
}

// --- tool methods -------------------------------------------------------

// me() — current Figma account; doubles as the token validator.
// GET /v1/me — requires current_user:read.
export function me() {
  var r = _get("/me");
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var u = r.body || {};
  return { ok: true, id: u.id, handle: u.handle, email: u.email, imgUrl: u.img_url };
}

// getFileMeta(fileKey) — lightweight file metadata (cheap; use for
// "exists / has it changed?" checks before pulling the tree).
// GET /v1/files/:key/meta — requires file_metadata:read.
export function getFileMeta(fileKey) {
  var key = _parseFileKey(fileKey);
  if (!key) return { ok: false, error: "fileKey (or a figma.com file URL) is required" };
  var r = _get("/files/" + key + "/meta");
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var f = (r.body && r.body.file) ? r.body.file : (r.body || {});
  return {
    ok: true,
    fileKey: key,
    name: f.name,
    lastModified: f.last_touched_at || f.lastModified,
    editorType: f.editor_type || f.editorType,
    thumbnailUrl: f.thumbnail_url || f.thumbnailUrl
  };
}

// listComments(fileKey) — all comments on a file, markdown-rendered, threaded
// via parent_id, newest-first as Figma returns them.
// GET /v1/files/:key/comments?as_md=true — requires file_comments:read.
export function listComments(fileKey) {
  var key = _parseFileKey(fileKey);
  if (!key) return { ok: false, error: "fileKey (or a figma.com file URL) is required" };
  var r = _get("/files/" + key + "/comments?as_md=true");
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var raw = (r.body && r.body.comments) ? r.body.comments : [];
  var comments = [];
  for (var i = 0; i < raw.length; i++) {
    var c = raw[i];
    var user = c.user || {};
    comments.push({
      id: c.id,
      author: user.handle,
      message: c.message,
      createdAt: c.created_at,
      resolvedAt: c.resolved_at || null,
      parentId: c.parent_id || null
    });
  }
  return { ok: true, fileKey: key, count: comments.length, comments: comments };
}

// getFileText(fileKey, opts) — fetch the depth-limited node tree and return
// every TEXT layer's content. NEVER returns the raw tree. opts: { depth?
// (default 8) }. GET /v1/files/:key?depth=N — requires file_content:read.
export function getFileText(fileKey, opts) {
  var key = _parseFileKey(fileKey);
  if (!key) return { ok: false, error: "fileKey (or a figma.com file URL) is required" };
  opts = opts || {};
  var depth = (typeof opts.depth === "number" && opts.depth > 0) ? Math.floor(opts.depth) : DEFAULT_DEPTH;
  var r = _get("/files/" + key + "?depth=" + depth);
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var doc = r.body || {};
  var out = [];
  _walkText(doc.document, "", out);
  return {
    ok: true,
    fileKey: key,
    name: doc.name,
    lastModified: doc.lastModified,
    depth: depth,
    textNodes: out
  };
}

// listProjectFiles(projectId) — files in a Figma project. NOTE: requires the
// projects:read scope, which is private-OAuth-app-only — a plain PAT usually
// 403s here. GET /v1/projects/:project_id/files.
export function listProjectFiles(projectId) {
  if (!projectId || typeof projectId !== "string") return { ok: false, error: "projectId is required" };
  var r = _get("/projects/" + projectId + "/files");
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var files = (r.body && r.body.files) ? r.body.files : [];
  return { ok: true, projectId: projectId, name: r.body && r.body.name, count: files.length, files: files };
}

// listTeamProjects(teamId) — projects in a team. teamId comes from a team URL
// (figma.com/team/:team_id/...). NOTE: requires projects:read (private-OAuth-app
// -only) — a plain PAT usually 403s here. GET /v1/teams/:team_id/projects.
export function listTeamProjects(teamId) {
  if (!teamId || typeof teamId !== "string") return { ok: false, error: "teamId is required" };
  var r = _get("/teams/" + teamId + "/projects");
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var projects = (r.body && r.body.projects) ? r.body.projects : [];
  return { ok: true, teamId: teamId, name: r.body && r.body.name, count: projects.length, projects: projects };
}

// main(args) — default entry point: a quick connectivity check (me).
export function main(args) {
  return me();
}
