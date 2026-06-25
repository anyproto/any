# attio

## Tool Description

Read-only access to the user's **Attio** CRM (people, companies, deals, lists,
notes, workspace members). Tagged `integration`. Use it to pull CRM context into
the second brain and to answer questions about contacts, companies, and deals —
e.g. "who do we know at <company>", "what's the status of the <X> deal", "what
did we last note about <person>". Ingestion only: it never modifies Attio data.

**Credential setup.** This connector authenticates with a personal API access
token. The user creates one in Attio under **Workspace settings → Developers**
(developers.attio.com): create a new integration / access token, name it (e.g.
"bobrik / any"), and copy the token string once. The token is stored in
`config@v1` as `ATTIO_API_TOKEN`. Every request carries
`Authorization: Bearer <token>`.

**REQUIRED READ SCOPES (critical — the #1 reason it fails on first try).** Attio
tokens default to **NO scopes** and silently return **403** until the user grants
read scopes on the integration. For read-only ingestion the user must enable, at
minimum: `record_permission:read`, `object_configuration:read`,
`list_entry:read`, `user_management:read`, and `note:read`. Scopes are editable
on an existing token later, so the user can start broad-read and tighten. If a
call returns 403, tell the user to add these scopes in Workspace settings →
Developers — do not assume the token itself is bad.

**Usage guidance.** Attio workspaces are heavily customizable, so drive off
`listObjects()` / `listAttributes(object)` rather than hardcoding slugs (the
standard slugs are `people`, `companies`, `deals`; `deals` may be disabled). Run
`whoami()` first to confirm the token + scopes work. Record values come back
under a `values` map keyed by attribute slug, and most values are **arrays of
historized value objects** (Attio versions attribute values), so read the active
entry (typically `values.<slug>[0]`) defensively rather than assuming a scalar.
Record and list-entry queries page with `limit` (max 500) + `offset`; defaults
here are small to protect context — bump `offset` until a page shorter than
`limit` comes back. The connector backs off on HTTP 429 (honoring `Retry-After`).

## Tool Schema

### whoami() [getter]
Credential/scope sanity check. There is no dedicated "self" endpoint for a token,
so this lists workspace members to confirm the token + scopes work and surfaces a
friendly error early. Params: none. Returns `{ok, connected, memberCount,
members}` or `{ok:false, error}` (the error enumerates the required scopes on a
403).

### listObjects() [getter]
List the objects (tables) configured in the workspace and their slugs
(people/companies/deals/custom). `GET /v2/objects`. Params: none. Returns
`{ok, objects}`.

### listAttributes(object) [getter]
List the attributes (columns) of one object — the schema you need to know which
attribute slugs to read/map. `GET /v2/objects/{object}/attributes`. Params:
`object` (string, slug or UUID, required). Returns `{ok, attributes}`.

### queryRecords(object, opts) [getter]
Query records of an object with optional filter/sort and pagination — the
workhorse for people/companies/deals. `POST /v2/objects/{object}/records/query`.
Params: `object` (string, slug or UUID, required); `opts` =
`{ filter? (Attio shorthand `{slug: value}` ⇒ $eq, or verbose
$and/$or/$not + per-attribute operators), sorts? ([{attribute, direction:
"asc"|"desc"}]), limit? (number, default 100, max 500), offset? (number,
default 0) }`. Returns `{ok, records, count, offset, limit}`.

Example — `queryRecords("people", { filter: { "$or": [ { "name": { "$contains":
"Smith" } } ] }, sorts: [{ attribute: "name", direction: "asc" }], limit: 50 })`
issues:

```jsonc
// POST https://api.attio.com/v2/objects/people/records/query
// Authorization: Bearer <token>
// Content-Type: application/json
{
  "filter": { "$or": [ { "name": { "$contains": "Smith" } } ] },
  "sorts":  [ { "attribute": "name", "direction": "asc" } ],
  "limit":  50,
  "offset": 0
}
```

Response envelope is `{ "data": [ { "id": {workspace_id, object_id, record_id},
"values": { … } }, … ] }`; page by bumping `offset` until a short page returns.

### getRecord(object, recordId) [getter]
Fetch a single record by its record_id UUID.
`GET /v2/objects/{object}/records/{record_id}`. Params: `object` (string,
required), `recordId` (string UUID, required). Returns `{ok, record}`.

### listLists() [getter]
List the lists (pipelines/segments) in the workspace. `GET /v2/lists`. Params:
none. Returns `{ok, lists}`.

### queryListEntries(list, opts) [getter]
Query entries of one list (pipeline/segment).
`POST /v2/lists/{list}/entries/query`. Params: `list` (string, slug or UUID,
required); `opts` same shape as `queryRecords` (`filter`, `sorts`, `limit`,
`offset`). Returns `{ok, entries, count, offset, limit}`.

### listNotes(opts) [getter]
List notes (free-text context attached to records), paginated. `GET /v2/notes`.
Params: `opts` = `{ limit? (number, default 50), offset? (number, default 0) }`.
Returns `{ok, notes, count, offset, limit}`.

### listWorkspaceMembers() [getter]
List workspace members (team roster) for owner/assignee resolution.
`GET /v2/workspace_members`. Params: none. Returns `{ok, members}`.
