// __main_source
// __tags: integration
// linear@v1 — read-only connector for the Linear issue tracker (GraphQL).
// Pattern-1 token connector: reads cfg.LINEAR_API_KEY from config@v1 and sends
// it RAW in the Authorization header (NO "Bearer " prefix — that's OAuth-only;
// a Bearer prefix on a personal key yields a 401 that looks like a bad key).
// Surfaces the user's assigned issues, workspace issues (optionally
// incremental by updatedAt), teams, single issues, and issue comments. All
// methods return a consistent {ok, ...} / {ok:false, error} shape.
// See dev space → Integration Docs → 02-linear.

import { main as getConfig } from "config@v1";

var ENDPOINT = "https://api.linear.app/graphql";
var KEY_URL = "https://linear.app/settings/api";
var DEFAULT_FIRST = 25;
var MAX_FIRST = 100;

// --- credential ---------------------------------------------------------

// _token() — resolve the personal API key from config@v1, or throw a clear,
// actionable "not connected" error the agent can relay (and offer to save).
function _token() {
  var t = getConfig().LINEAR_API_KEY;
  if (!t) {
    throw new Error("Linear not connected — create a personal API key at "
      + KEY_URL + " and add it to config@v1 as LINEAR_API_KEY "
      + "(or paste it to me and I'll save it).");
  }
  return t;
}

// _clampFirst(n) — keep page sizes sane: default small, hard-cap at MAX_FIRST.
// Linear's complexity rate limit multiplies a connection's cost by `first`, so
// a careless huge page can trip the per-query ceiling.
function _clampFirst(n) {
  if (typeof n !== "number" || !(n > 0)) return DEFAULT_FIRST;
  if (n > MAX_FIRST) return MAX_FIRST;
  return Math.floor(n);
}

// --- GraphQL transport --------------------------------------------------

// _gql(query, variables) — single POST to the GraphQL endpoint. Returns
// { ok, data } on success or { ok:false, error, status } on failure. Inspects
// BOTH the HTTP status AND body.errors, because Linear (like most GraphQL
// servers) returns HTTP 200 with an `errors[]` array for many failures — a
// status-only check would silently swallow them. Auth failures surface as
// 400/401; the message points back at the key.
function _gql(query, variables) {
  var key;
  try {
    key = _token();
  } catch (e) {
    return { ok: false, error: e && e.message ? e.message : String(e) };
  }

  var resp;
  try {
    resp = fetch(ENDPOINT, {
      method: "POST",
      headers: {
        "Authorization": key,
        "Content-Type": "application/json"
      },
      body: JSON.stringify({ query: query, variables: variables || {} })
    });
  } catch (e) {
    return { ok: false, error: "fetch failed: " + (e && e.message ? e.message : String(e)) };
  }

  if (!resp) return { ok: false, error: "no response from Linear" };

  var body = resp.body;

  if (!resp.ok) {
    if (resp.status === 401 || resp.status === 400) {
      return {
        ok: false,
        status: resp.status,
        error: "Linear rejected the API key (HTTP " + resp.status + "). "
          + "Check LINEAR_API_KEY in config@v1 — send it RAW, no Bearer prefix. "
          + "Create a new key at " + KEY_URL + " if needed."
      };
    }
    var hmsg = (body && body.errors) ? JSON.stringify(body.errors)
      : "HTTP " + resp.status;
    return { ok: false, status: resp.status, error: hmsg };
  }

  if (body && body.errors) {
    return { ok: false, status: resp.status, error: "Linear: " + JSON.stringify(body.errors) };
  }
  if (!body || !body.data) {
    return { ok: false, status: resp && resp.status, error: "empty GraphQL response" };
  }
  return { ok: true, data: body.data };
}

// _conn(connection) — normalize a Relay connection { nodes, pageInfo } into
// { nodes, hasNextPage, endCursor } with safe fallbacks.
function _conn(connection) {
  var nodes = (connection && connection.nodes) ? connection.nodes : [];
  var pi = (connection && connection.pageInfo) ? connection.pageInfo : {};
  return {
    nodes: nodes,
    hasNextPage: !!pi.hasNextPage,
    endCursor: pi.endCursor || null
  };
}

// --- shared GraphQL fragments (string concat, house style) --------------

var ISSUE_FIELDS =
  "id identifier title priority url createdAt updatedAt "
  + "state { name type } "
  + "assignee { id name } "
  + "team { id name key }";

var ISSUE_FIELDS_FULL = ISSUE_FIELDS + " description";

// --- tool methods -------------------------------------------------------

// whoami() — current Linear user; doubles as the key validator.
export function whoami() {
  var q = "query Whoami { viewer { id name email } }";
  var r = _gql(q, {});
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var v = r.data.viewer;
  if (!v) return { ok: false, error: "no viewer in response (key may be invalid)" };
  return { ok: true, user: { id: v.id, name: v.name, email: v.email } };
}

// myIssues(opts) — issues assigned to the current user, newest-first.
// opts: { first?, after? }.
export function myIssues(opts) {
  opts = opts || {};
  var first = _clampFirst(opts.first);
  var q = "query MyIssues($first: Int!, $after: String) {"
    + " viewer { id assignedIssues(first: $first, after: $after, orderBy: updatedAt) {"
    + " nodes { " + ISSUE_FIELDS + " } pageInfo { hasNextPage endCursor } } } }";
  var r = _gql(q, { first: first, after: opts.after || null });
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var viewer = r.data.viewer || {};
  var c = _conn(viewer.assignedIssues);
  return { ok: true, issues: c.nodes, hasNextPage: c.hasNextPage, endCursor: c.endCursor };
}

// listIssues(opts) — issues across the workspace, newest-first. Optionally
// incremental: opts.updatedAfter (ISO-8601) keeps only updatedAt >= it.
// opts: { first?, after?, updatedAfter? }.
export function listIssues(opts) {
  opts = opts || {};
  var first = _clampFirst(opts.first);
  var vars = { first: first, after: opts.after || null };
  var q;
  if (opts.updatedAfter) {
    vars.since = opts.updatedAfter;
    q = "query ListIssues($first: Int!, $after: String, $since: DateTimeOrDuration) {"
      + " issues(first: $first, after: $after, orderBy: updatedAt,"
      + " filter: { updatedAt: { gte: $since } }) {"
      + " nodes { " + ISSUE_FIELDS + " } pageInfo { hasNextPage endCursor } } }";
  } else {
    q = "query ListIssues($first: Int!, $after: String) {"
      + " issues(first: $first, after: $after, orderBy: updatedAt) {"
      + " nodes { " + ISSUE_FIELDS + " } pageInfo { hasNextPage endCursor } } }";
  }
  var r = _gql(q, vars);
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var c = _conn(r.data.issues);
  return { ok: true, issues: c.nodes, hasNextPage: c.hasNextPage, endCursor: c.endCursor };
}

// listTeams() — teams { id, name, key }. Capped at MAX_FIRST (the structural
// map is small; one page is plenty).
export function listTeams() {
  var q = "query ListTeams($first: Int!) {"
    + " teams(first: $first) { nodes { id name key }"
    + " pageInfo { hasNextPage endCursor } } }";
  var r = _gql(q, { first: MAX_FIRST });
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var c = _conn(r.data.teams);
  return { ok: true, teams: c.nodes, hasNextPage: c.hasNextPage, endCursor: c.endCursor };
}

// getIssue(id) — one issue with full detail (includes description Markdown).
// `id` accepts the issue id or its identifier (e.g. "ENG-123").
export function getIssue(id) {
  if (!id || typeof id !== "string") return { ok: false, error: "id is required" };
  var q = "query GetIssue($id: String!) {"
    + " issue(id: $id) { " + ISSUE_FIELDS_FULL + " } }";
  var r = _gql(q, { id: id });
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var issue = r.data.issue;
  if (!issue) return { ok: false, error: "issue not found: " + id };
  return { ok: true, issue: issue };
}

// listComments(opts) — comments on an issue, oldest-first.
// opts: { issueId (required), first?, after? }.
export function listComments(opts) {
  opts = opts || {};
  if (!opts.issueId) return { ok: false, error: "issueId is required" };
  var first = _clampFirst(opts.first || 50);
  var q = "query ListComments($id: String!, $first: Int!, $after: String) {"
    + " issue(id: $id) { id identifier comments(first: $first, after: $after) {"
    + " nodes { id body createdAt updatedAt url user { id name } }"
    + " pageInfo { hasNextPage endCursor } } } }";
  var r = _gql(q, { id: opts.issueId, first: first, after: opts.after || null });
  if (!r.ok) return { ok: false, error: r.error, status: r.status };
  var issue = r.data.issue;
  if (!issue) return { ok: false, error: "issue not found: " + opts.issueId };
  var c = _conn(issue.comments);
  return { ok: true, issueId: issue.id, identifier: issue.identifier,
    comments: c.nodes, hasNextPage: c.hasNextPage, endCursor: c.endCursor };
}

// main(args) — default entry point: a quick connectivity check (whoami).
export function main(args) {
  return whoami();
}
