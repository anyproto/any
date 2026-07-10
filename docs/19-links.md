# 19 — Canonical `any://` link format

The one string convention for referencing anything inside `any` from inside
`any`: an object, an identity, a space, a property value, a dataset record, a
file. One grammar, **self-describing** (a reader can tell *what* a URI points at
without a lookup), extensible without a v2, and small enough to sit unescaped
inside a markdown link destination.

The format lives in the public `anyuri` package of this repo —
`github.com/anyproto/any/anyuri`, the deliberate exception to the
everything-under-`internal/` rule: `any` owns the format. Clients and agents
**import the rule (Build/Parse/IsValid/ExtractMentions), they do not
reimplement it**. This doc is the contract the client teams (desktop, mobile,
bao) consume; the package is the source of truth for the grammar, this is the
source of truth for the semantics.

Mentions (editor + chat) are just one link kind riding on this format — see
[docs/16-chat.md](16-chat.md) for the chat mentions field. This doc is only the
link format.

## The grammar

```
any://<kind>/<spaceId>[/<rest…>][?<params>][#<fragment>]
```

The **kind** is the first path segment. It makes the target self-describing:
prefix-match `any://o/` / `any://m/` / `any://f/` etc. and you know what you're
pointing at before you resolve anything. This is **type (b)** in the SYN-67
design — a path-prefix kind, chosen over the `?type=` query-param alternative
(type (a)) because it has no default-kind ambiguity and reads like a REST path.

Everything after the kind is **kind-specific** but follows one composition rule
(below). `spaceId` is present in every kind — links are global by design; there
is no in-space short form in the typed grammar (the legacy bare forms are the
one exception, see [Back-compat](#back-compat)).

## Kind registry

| Kind | Shape | Points at |
|------|-------|-----------|
| `o` | `any://o/<spaceId>/<objectId>` | an object |
| `o` | `any://o/<spaceId>/<objectId>/<dataset>/<recordId>` | a **record** inside an object's dataset (editor block, chat message) |
| `o` | `any://o/<spaceId>/<objectId>/<dataset>/<recordId>/<propId>` | *(reserved)* a field on a record |
| `m` | `any://m/<spaceId>/<identity>` | an identity / member (a **mention**) |
| `s` | `any://s/<spaceId>` | a space |
| `p` | `any://p/<spaceId>/<objectId>/<propId>` | a **property value** on an object |
| `f` | `any://f/<spaceId>/<fileId>` | a file (files-v2 attachment) |
| `i` | *reserved, not implemented* | an invite deep link |

The kind set is an **open slug set** — new kinds get added here; parsers must
not assume the table is closed (see [Extension policy](#extension-policy)).

Kind slugs occupy a **reserved lexical namespace**: a first path segment of
**1-4 lowercase-alphanumeric bytes** (`[a-z0-9]{1,4}`) is always a kind slug;
anything longer is a legacy bare id (see [Back-compat](#back-compat)). Real
ids are ≥40-char base58 strings, so no collision is possible. Two
consequences: a future kind must keep its slug within that shape, and a
pathological ≤4-char object id no longer parses in the bare form — no real id
is that short.

### `o` — objects and their dataset records

The object is the addressing root. A bare `any://o/<spaceId>/<objectId>` points
at the object itself. Appending a `<dataset>/<recordId>` pair walks into one of
its datasets — this is the **path composition rule**, and it is uniform:

```
any://o/<spaceId>/<objectId>/editor_blocks/<blockId>    a block in a document
any://o/<spaceId>/<objectId>/chat_messages/<msgId>      a message in a chat
```

`<dataset>` is the dataset name (`editor_blocks`, `chat_messages`, …) exactly as
it appears in `/query`'s `dataset` field; `<recordId>` is the record's derived
id. **One rule covers editor blocks and chat messages alike** — dataset-name +
record-id, same shape everywhere. This replaces the old `#block` fragment hack
(see [Fragments](#fragments)).

A trailing `<propId>` segment (`…/<dataset>/<recordId>/<propId>`) to address a
field *within* a record is **reserved** — the grammar leaves room for it, it is
not required by any current consumer.

### `m` — mentions

```
any://m/<spaceId>/<identity>
```

`<identity>` is the account identity string. A mention in message or block text
is a markdown link whose destination is an `m` URI:

```
Hey [Zarko](any://m/<spaceId>/<identity>), take a look
```

The link **text** is the display name *at time of writing* — a snapshot, and the
fallback a non-aware renderer (raw markdown, export, grep) shows. A
mention-aware client detects the `m/` prefix, resolves the current display name
via `GET /v1/identities`, and renders the "magic" mention. Identity profiles are
encrypted — tolerate unresolved / id-only until the `symKey` arrives; fall back
to the snapshot link text. See SYN-72 (chat mentions field) and SYN-73 (any-ui
rendering).

### `s` — spaces

```
any://s/<spaceId>
```

The whole space. No further segments.

### `p` — property values

```
any://p/<spaceId>/<objectId>/<propId>
```

**This property's value on this object** — the "I took this figure from this
property" citation target. It is a *value* reference (per-object, per-prop), not
the property *definition*. How a client renders it (open the object, highlight
the field) is a UI question; the format's job is only to carry object + prop.
Keyed by the content-addressed `propId`, never `xKey` (xKey is client-side only
— see CLAUDE.md).

> **Note on `p` vs links-format property values.** A links-format *property
> value* stores the plain legacy `any://<objectId>` form (see
> [Back-compat](#back-compat)) — that is a value *inside* a property, and it
> keeps its bare shape. The `p` kind is the inverse: a link that *points at* a
> property value from somewhere else.

### `f` — files

```
any://f/<spaceId>/<fileId>                 file (files-v2 attach)
any://f/<spaceId>/<fileId>?variant=thumb   variant by tag
```

Files get their own kind rather than the `o/<dataset>/<recordId>` form,
because a file's `payloads` row lives on a **hidden derived child object**
reachable only through the bespoke `/files/query` bridge — it is not
generic-query-reachable, so it is not a dataset-record path. Details that matter
to every consumer:

- **`fileId`, not `rootCid`.** `fileId` is the payloads-row id: change-derived,
  per-attach, space-unique — the handle every files endpoint takes. `rootCid` is
  the encrypted-content address (absent for inline files, shared across dedup'd
  attaches) and **never appears in a URI**.
- **Per-attach identity is intentional.** A re-upload is a new `fileId`; a link
  means "*this* attachment, as shared". Content-level refcounting (GC / offload
  safety) counts payloads rows sharing a `rootCid` — the format keeps that
  separation clean.
- **Consumers.** Chat `attachments` entries and inline message text use this
  URI; editor image/file markdown destinations use it
  (`![alt](any://f/<sp>/<fileId>)`). A client detects the `f/` prefix and
  fetches bytes via `GET /v1/spaces/:s/files/:fileId/content`.
- **Variants** by tag: `?variant=thumb` (see [Params](#params)).
- **Sealed meta** (`name` / `mime`) is member-only — a renderer without the key
  falls back to the markdown link-text snapshot, the same rule as mentions.

See [docs/17-files.md](17-files.md) for the storage model.

## Params

Query params carry kind-specific modifiers that aren't part of the identity of
the target:

```
any://f/<spaceId>/<fileId>?variant=thumb
```

An unknown param must be ignored, never a parse failure — same graceful-degrade
rule as unknown kinds.

## Fragments

`#fragment` is for **view-level anchors only** — "scroll here" — and **never**
for identifying a data record.

```
any://o/<spaceId>/<debugObjectId>#turn_3      scroll to turn 3 on the debug page
```

`#turn_3` says *where to scroll on a rendered page*; it does not name a record.
Anything that identifies data goes in the **path** (`…/<dataset>/<recordId>`).

This is the rule the old enrichment `#block` hack violated — it crammed a block
id (`any://<objectId>#<blockId>`) into the fragment. A block is a *record* of the
`editor_blocks` dataset, not an opaque anchor: blocks re-render and restructure.
That producer migrates to the dataset-record path form
(`any://o/<sp>/<obj>/editor_blocks/<blockId>`) — see WEB-42.

## Extension policy

The kind registry is open. Adding a kind must not break an existing parser:

- **Unknown kind ⇒ degrade to a plain link.** A parser that doesn't recognize
  the first segment treats the whole thing as an ordinary (non-magic) link and
  renders the markdown link text. It does **not** error, and it does **not**
  guess. In the `anyuri` API this is the distinct sentinel `ErrKindUnknown`
  (degrade) vs `ErrInvalid` (reject) — the two never wrap each other;
  classify with `errors.Is`.
- **Unknown param ⇒ ignore it** (above).
- Prefix-match on the kind, don't heuristically sniff id shapes. The kind is
  explicit precisely so no consumer has to guess from an id's encoding. New
  slugs must fit the reserved lexical namespace (`[a-z0-9]{1,4}`, see
  [Kind registry](#kind-registry)).

This is what lets `i` (invite) and future kinds land later without a format v2 or
a client redeploy.

## Back-compat

The pre-typed **bare forms** stay valid and keep meaning **object**:

```
any://<objectId>              in-space object reference (property-value form)
any://<spaceId>/<objectId>    global object reference
```

These are what's stored today and must keep parsing:

- **Links-format property values** (`space.FormatLinks`,
  `internal/server/propformat.go`): strictly the one-segment, fragment-less form
  `any://<objectId>`. Value writes are validated against exactly this shape.
- **Backlinks** (`internal/api/backlinks.go`, `handlers_backlinks.go`): reverse
  lookup keyed on that same one-segment form.
- **Agent `debugLink`** on chat messages: `any://<spaceId>/<objectId>#turn_<n>`.

`Parse` accepts the bare forms as kind `o` (flagged `Legacy: true`);
`BuildObject` for a fresh object reference still emits the bare property-value
form where the stricter consumers require it (`IsPropertyValueRef` is that
strict check). No stored value is rewritten — the typed grammar is a superset,
the bare object form is its shorthand. Disambiguation is the kind-slug lexical
rule ([Kind registry](#kind-registry)): a first segment longer than 4 bytes is
an id, not a kind. New typed references (mentions, files, property-value
citations, dataset records) always use the explicit-kind form.

## Explicitly out of scope

- **Invite deep links.** Invites are shared as raw base58 text today; a
  clickable invite link needs an HTTP front (an `https://` URL that
  redirects/deeplinks into the app) first — a separate effort. The `i` kind is
  **reserved** in this registry; it is not implemented.
- **Inline views / transclusion** — embedding a live property value or a
  filtered object set inline in a document. That is not a link, it's a
  macro/embed block: it doesn't fit a markdown link destination and needs
  product design for the block representation. A transclusion will *contain* an
  `any://` URI as its target — it is not itself one. The link format's only job
  here is not to paint us into a corner.

## Where the code lives

| Concern | Location |
|---------|----------|
| Grammar (builders / `Parse` / `IsValid` / `IsPropertyValueRef`, kind constants) | `anyuri/` — public `github.com/anyproto/any/anyuri` |
| Mention extraction from text (`ExtractMentions`) | `anyuri/mentions.go` — the sanctioned scanner, shared by server derivation and clients |
| Links-format property-value validation | `internal/server/propformat.go` |
| Backlinks reverse lookup | `internal/api/backlinks.go`, `internal/server/handlers_backlinks.go` |
| Chat mentions derivation (server-parsed `mentions` field) | SYN-72 — see [docs/16-chat.md](16-chat.md) |
| File links | [docs/17-files.md](17-files.md) |
