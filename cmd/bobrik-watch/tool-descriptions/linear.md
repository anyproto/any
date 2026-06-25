# linear

## Tool Description

Read the user's **Linear** workspace — the team's issue tracker, the canonical
record of *what work is happening and why*. Tagged `integration`. Read-only in
this MVP: it surfaces the user's assigned issues, all workspace issues
(optionally incrementally, by `updatedAt`), teams, individual issues with full
detail, and issue comments. It does **not** create or modify anything in Linear.

Use it to answer "what's on my plate," draft standups, pull project/team
structure, or fetch the prose of an issue and its discussion (descriptions +
comments are dense, decision-bearing — good recall fodder, e.g. "what did we
decide about the auth refactor?"). Issues come back with `identifier` (e.g.
`ENG-123`), `title`, `priority` (0 none / 1 urgent / 2 high / 3 normal / 4 low),
`state.name` (the status label: Backlog → In Progress → Done), `assignee`,
`team`, `url`, and timestamps; `getIssue` and `listComments` add the Markdown
`description` and comment bodies.

**Credential setup (Pattern 1 — personal API key).** The user creates a key at
**https://linear.app/settings/api** (Settings → Security & access → Personal API
keys → "New API key", copy once), and it's stored in `config@v1` under
`LINEAR_API_KEY`. The key is sent **raw** in the `Authorization` header — there
is **no `Bearer` prefix** (that's OAuth-only; adding it yields a 401 that looks
like a bad key). A personal key inherits the user's full permissions (no scope
picker). If the key is absent, every method returns a clear "not connected"
error pointing at that URL — relay it and offer to save a pasted key into
`config@v1`. Run `whoami()` first to validate the key (a 400/401 there means the
stored key is bad).

**Pagination & limits.** Linear uses Relay cursor connections: list methods take
`first` (page size, default 25, capped at 100) and `after` (an opaque cursor);
each result carries `hasNextPage` and `endCursor` — pass `endCursor` back as
`after` to page. Keep `first` modest: Linear rate-limits by query *complexity*
(3M points/hour for personal keys, 10k-point per-query ceiling), and a
connection multiplies its children's cost by `first`. For incremental sync over
a large tracker, prefer `listIssues({ updatedAfter })` over a full crawl. All
methods return `{ ok, ... }` on success or `{ ok: false, error, status? }` on
failure.

## Tool Schema

### whoami() [getter]

Return the current Linear user and validate the API key. Doubles as the
credential check — call it first.

- (no arguments)

Output: `{ ok, user: { id, name, email } }` or `{ ok: false, error }`.

```js
var me = linear.whoami();
me.user.name   // "Ada Lovelace"
```

### myIssues(opts) [getter]

List issues assigned to the current user, newest-first (by `updatedAt`).

- `opts` (object, optional):
  - `first` (number, default 25, max 100) — page size.
  - `after` (string, optional) — pagination cursor from a previous call.

Output: `{ ok, issues, hasNextPage, endCursor }`. Each issue:
`{ id, identifier, title, priority, url, createdAt, updatedAt, state {name,type}, assignee {id,name}, team {id,name,key} }`.

```js
var r = linear.myIssues({ first: 25 });
r.issues[0].identifier;                 // "ENG-123"
if (r.hasNextPage) linear.myIssues({ after: r.endCursor });
```

### listIssues(opts) [getter]

List issues across the whole workspace, newest-first. Optionally incremental:
pass `updatedAfter` to keep only issues touched at or after a timestamp — the
recommended way to sync a large tracker without re-crawling it.

- `opts` (object, optional):
  - `first` (number, default 25, max 100) — page size.
  - `after` (string, optional) — pagination cursor.
  - `updatedAfter` (string, optional) — ISO-8601 timestamp; only issues with `updatedAt >= this`.

Output: `{ ok, issues, hasNextPage, endCursor }` — issue shape as in `myIssues`.

```js
// Everything changed since yesterday:
linear.listIssues({ updatedAfter: "2026-06-23T00:00:00Z", first: 50 });
```

### listTeams() [getter]

List the workspace's teams — the structural map.

- (no arguments)

Output: `{ ok, teams, hasNextPage, endCursor }`; each team `{ id, name, key }`.

```js
linear.listTeams().teams;   // [{ id, name: "Engineering", key: "ENG" }, ...]
```

### getIssue(id) [getter]

Fetch one issue with full detail, including the Markdown `description`.

- `id` (string, required) — the issue id or its identifier (e.g. `"ENG-123"`).

Output: `{ ok, issue }` where `issue` is the full issue object plus
`description`, or `{ ok: false, error }` if not found.

```js
var iss = linear.getIssue("ENG-123").issue;
iss.title; iss.description;   // Markdown body
```

### listComments(opts) [getter]

List the comments on an issue, oldest-first.

- `opts` (object):
  - `issueId` (string, required) — issue id or identifier.
  - `first` (number, default 50, max 100) — page size.
  - `after` (string, optional) — pagination cursor.

Output: `{ ok, issueId, identifier, comments, hasNextPage, endCursor }`. Each
comment: `{ id, body, createdAt, updatedAt, url, user {id,name} }`.

```js
var c = linear.listComments({ issueId: "ENG-123" });
c.comments[0].body;   // first comment text
```
