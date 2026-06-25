# gmail

## Tool Description

Read the user's **Gmail** mailbox — the single richest source of working context
(who they talk to, what's in flight, decisions, commitments, receipts, vendor
and teammate threads). Tagged `integration`. **Read-only** in this MVP: it lists
and fetches messages, threads, and labels; it never sends, archives, deletes, or
labels mail. Use it to answer "what did $person say about $topic", "summarize my
last week of mail", or to pull a thread for recall.

**Depends on `googleAuth` — connect first.** Gmail is OAuth, *not* a token
connector: this tool does **no** OAuth itself. It rides the shared `googleAuth`
layer (one Google OAuth client / one consent / one refresh token shared across
`gmail`, `googleSheets`, `googleCalendar`, `googleDrive`). Before any method
works, the user must run **`googleAuth.connect()`** once (which opens the browser
consent screen and returns a refresh token to save to `config@v1` as
`GOOGLE_OAUTH_REFRESH_TOKEN`). After that, access tokens are refreshed
transparently inside `googleAuth` — this tool just calls authed GETs. If a
method returns a not-connected error, relay it: the fix is to run
`googleAuth.connect()`.

**Scope & verification caveat.** Gmail reads use the `gmail.readonly` scope
(already in `googleAuth`'s default scope union). That is a **sensitive** Google
scope: until the OAuth app is submitted for Google verification it runs in
"Testing" mode — capped at ≤100 test users, refresh tokens expire after ~7 days,
and the consent screen shows an "unverified app" warning. Fine for a single-user
local-first setup; a public release needs Google's OAuth verification. (Staying
on `readonly` deliberately avoids the *restricted* `https://mail.google.com/`
scope, which would trigger an annual CASA security assessment.)

**The `q:` search syntax.** `search()` and `listMessages({q})` accept Gmail's
search-query language, the same as the Gmail search box: `from:`, `to:`,
`subject:`, `is:unread`, `is:starred`, `has:attachment`, `label:`,
`newer_than:7d` / `older_than:1y`, `after:YYYY/MM/DD` / `before:YYYY/MM/DD`,
boolean `OR` and `-` negation. Example: `from:boss newer_than:14d is:unread`.

**Pagination & limits.** `listMessages`/`search` return only `{id, threadId}`
stubs plus a `nextPageToken`; pass it back as `pageToken` (with the *same* other
params) to page — there is no backward token. `maxResults` defaults to 20 and is
hard-capped at 100 (Gmail allows 500, but each `getMessage` costs 20 quota units,
so wide fan-outs burn the per-user budget — page narrow). `getMessage`/
`getThread` return **trimmed** objects (decoded headers + snippet + the decoded
`text/plain` body, falling back to stripped HTML) — never the raw MIME payload
tree, to keep agent context small. All methods return `{ ok, ... }` on success
or `{ ok: false, error, status? }` on failure (HTTP 429 quota errors surface as
an error — back off and retry the call).

## Tool Schema

### listMessages(opts) [getter]

List message stubs matching a Gmail search query and/or labels. Returns only
`{id, threadId}` per message plus a page token — fetch full bodies with
`getMessage`.

- `opts` (object, optional):
  - `q` (string, optional) — Gmail `q:` search query (see the syntax above).
  - `labelIds` (string[], optional) — restrict to these label ids (e.g. `["INBOX"]`).
  - `maxResults` (number, default 20, max 100) — page size.
  - `pageToken` (string, optional) — `nextPageToken` from a previous call.

Output: `{ ok, messages: [{id, threadId}], nextPageToken, resultSizeEstimate }`.

```js
var r = gmail.listMessages({ q: "is:unread", maxResults: 25 });
r.messages[0].id;
if (r.nextPageToken) gmail.listMessages({ q: "is:unread", pageToken: r.nextPageToken });
```

### search(query, opts) [getter]

Thin convenience wrapper over `listMessages` taking the Gmail `q:` query as the
first positional arg. Same return shape as `listMessages`.

- `query` (string, required) — Gmail search syntax, e.g. `"from:boss newer_than:7d"`.
- `opts` (object, optional): `{ labelIds?, maxResults?, pageToken? }` — as `listMessages`.

Output: `{ ok, messages: [{id, threadId}], nextPageToken, resultSizeEstimate }`.

```js
gmail.search("from:notifications@github.com newer_than:3d").messages;
```

### getMessage(id, opts) [getter]

Fetch one message and decode it into a trimmed, readable shape: ids + labels +
parsed headers (From/To/Cc/Subject/Date) + snippet + the decoded `text/plain`
body (base64url-decoded; falls back to stripped HTML if there's no plain part).
The raw MIME payload tree is dropped.

- `id` (string, required) — the message id (from a stub).
- `opts` (object, optional):
  - `format` (string, default `"full"`) — `"full"` (headers + body), `"metadata"` (headers + snippet, no body), or `"minimal"` (ids/labels only).

Output: `{ ok, message: { id, threadId, labelIds, internalDate, from, to, cc, subject, date, snippet, body } }`.

```js
var m = gmail.getMessage("18f9c2...").message;
m.subject; m.from; m.body;   // decoded text/plain
```

### getThread(id) [getter]

Fetch every message in a thread (`format=full`), each in the same trimmed shape
as `getMessage`, in order.

- `id` (string, required) — the thread id (a message's `threadId`).

Output: `{ ok, id, historyId, messages: [ <trimmed message> ] }`.

```js
var t = gmail.getThread("18f9c2...");
t.messages.length; t.messages[0].body;
```

### listLabels() [getter]

List the account's labels — system (`INBOX`, `SENT`, `UNREAD`, …) and
user-created — to resolve names ↔ ids for `labelIds` filters. No pagination.

- (no arguments)

Output: `{ ok, labels: [{ id, name, type }] }` (`type` is `"system"` or `"user"`).

```js
gmail.listLabels().labels;   // [{ id:"INBOX", name:"INBOX", type:"system" }, ...]
```
