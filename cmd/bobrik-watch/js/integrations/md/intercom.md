# intercom

## Tool Description

`intercom@v1` reads a connected **Intercom** workspace into the second brain:
customer **conversations** (with full message transcripts), **contacts/leads**,
and help-center **articles**. Tagged `integration`. It is **read-only
(ingestion)** — replying to customers, tagging, and notes are deliberately out
of scope for v1 (real messages to real users are risky and not needed for the
context-engine goal). Use it to answer "what are users frustrated about this
week?", "who is this contact?", or "how do we tell customers to do X?".

**Credential — Pattern 1 (Access Token).** The tool reads its token from
`config@v1` as `INTERCOM_ACCESS_TOKEN`. Create it in the Intercom **Developer
Hub**: workspace → Settings → Developers → create (or open) a private app →
**Configure → Authentication** — the long-lived Access Token is shown there. It
is scoped to that one workspace; ensure the app has **read** scopes enabled for
conversations / contacts / articles or calls return 403. If the token is
missing, the tool returns a clear "not connected" error with this hint; paste
the token in chat and I'll save it into `config@v1` (plaintext-in-space is the
accepted v1 trade-off — the token is never logged).

**Required version header.** Every call sends three headers:
`Authorization: Bearer <token>`, `Accept: application/json`, and the
REQUIRED `Intercom-Version: 2.15` pin. The version header is mandatory by
convention — it keeps the response shape stable; omitting it falls back to the
app's default version, which can drift. The pin is bumped deliberately, not
automatically. Base URL is `https://api.intercom.io`; EU/AU-hosted workspaces
use `https://api.eu.intercom.io` / `https://api.au.intercom.io` (a region
mismatch fails auth confusingly — this connector defaults to the US base).

**Pagination & limits.** All list/search endpoints use **cursor pagination**:
read `pages.next.starting_after` from the response and pass it back as
`startingAfter` (list GETs as a query param, search POSTs inside the pagination
object) until it is absent. `perPage` defaults to 20, max 150. The tool backs
off on `429` honoring `Retry-After` and retries a bounded number of times.

**Data-volume caveat.** A mature workspace can hold **hundreds of thousands of
conversations**, and `list`/`search` return conversation **summaries WITHOUT
message parts** — the transcript requires a separate `getConversation(id)` call
(one GET per conversation). Do **not** blindly walk a whole workspace: default
to an **incremental window** with `searchConversations` filtered by
`updated_at > lastSyncTs`, and cap how many ids you fetch per run. Also note
`getConversation` returns only the **500 most recent parts** of a long thread
(older parts are silently dropped), and article bodies are HTML (light
cleanup needed before indexing).

## Tool Schema

### me() [getter]

Return the authenticated app/admin context — the cheapest connectivity / auth
check (use it to confirm the token works before a larger ingest).
- no inputs

Returns `{ok, me}` where `me` is the Intercom admin/app object, or
`{ok:false, error, status?}` on failure (e.g. 401 = bad/expired token).

```js
var r = intercom.me();
r.ok        // true if connected
r.me.email  // the admin's email
```

### listConversations(opts) [getter]

List conversation **summaries** (no message parts), most-recently-updated
first. Use `getConversation(id)` for the transcript.
- `opts.open?` (boolean) — only open (`true`) or closed (`false`) conversations
- `opts.sort?` (`"created_at" | "updated_at" | "waiting_since"`, default `updated_at`)
- `opts.order?` (`"asc" | "desc"`, default `desc`)
- `opts.perPage?` (number, default 20, max 150)
- `opts.startingAfter?` (string) — cursor from a previous page's `pages.next.starting_after`

Returns `{ok, conversations, pages}`.

```js
var r = intercom.listConversations({open: true, perPage: 50});
r.conversations.length;
var next = r.pages && r.pages.next && r.pages.next.starting_after; // cursor or undefined
```

### searchConversations(query, opts) [getter]

Filtered conversation search using the Intercom query DSL
(`{field, operator, value}`, with `AND`/`OR` groups; operators `=`, `!=`, `IN`,
`NIN`, `>`, `<`, `~`). Use for **incremental ingest**, e.g. `updated_at > lastSyncTs`.
- `query` (object, required) — the Intercom search query
- `opts.perPage?` (number, max 150)
- `opts.startingAfter?` (string) — cursor (sent in the pagination object)

Returns `{ok, conversations, pages}`.

```js
var r = intercom.searchConversations(
  {field: "updated_at", operator: ">", value: 1718200000},
  {perPage: 150}
);
```

### getConversation(id, opts) [getter]

Fetch one conversation **WITH** its message parts (the transcript).
- `id` (string, required) — conversation id
- `opts.plaintext?` (boolean, default `true`) — request `display_as=plaintext`
  bodies (already-stripped text; otherwise part bodies are HTML)

Returns `{ok, conversation}`; `conversation.conversation_parts.conversation_parts`
holds the parts (newest **500** only for long threads).

```js
var r = intercom.getConversation("123", {plaintext: true});
r.conversation.conversation_parts.conversation_parts;
```

### listContacts(opts) [getter]

Page contacts and leads (role `user` or `lead`) with email, name,
custom_attributes, last_seen_at, etc.
- `opts.perPage?` (number, max 150)
- `opts.startingAfter?` (string) — cursor

Returns `{ok, data, pages}` (contacts in `data`).

```js
var r = intercom.listContacts({perPage: 100});
r.data[0].email;
```

### searchContacts(query, opts) [getter]

Filtered contact search (e.g. by email or custom attribute) using the Intercom
query DSL.
- `query` (object, required) — the Intercom search query
- `opts.perPage?` (number, max 150)
- `opts.startingAfter?` (string) — cursor

Returns `{ok, data, pages}`.

```js
var r = intercom.searchContacts({field: "email", operator: "=", value: "a@b.com"});
```

### listArticles(opts) [getter]

Page help-center articles (`title`, `body` HTML, `state`, `author_id`, `url`).
- `opts.perPage?` (number, max 150)
- `opts.startingAfter?` (string) — cursor

Returns `{ok, data, pages}` (articles in `data`).

```js
var r = intercom.listArticles({perPage: 50});
r.data[0].title;
```
