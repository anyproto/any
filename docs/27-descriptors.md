# Property & field descriptors

One descriptor shape describes both **type properties** (values on an
object's row) and **dataset fields** (records in a per-object dataset).
Everything descriptive lives in a single `xFormat` object, which the SDK
stores **opaquely** — it is a client-facing contract, validated only by
`any`.

Endpoints and error codes: `03-api.md` § Types, § Runtime dataset
schemas, `06-errors.md`. SDK storage model: the SDK's
`docs/06-data-structure.md` § The `x-format` descriptor.

Spelling: the HTTP wire and PATCH paths use `xKey` / `xFormat`; the
stored record fields are `x-key` / `x-format` (visible in raw dataset
reads of a type object and in the SDK's Go API).

## The guarantee boundary

Read this first — every other rule follows from it, and it differs
between the two surfaces.

> **`kind` is the guarantee. `xFormat` is a hint.**

| | enforced on every peer at apply |
|---|---|
| **type property values** | the top-level `kind`, and nothing else |
| **dataset record fields** | `kind` recursively through `items` / `properties`, plus `required`, `mutableBy`, `stamp`, `idRule`, `deleteBy` |

A property value is checked against its declared `kind` at the top
level only, so a property's `items` / `properties` are **declarative**.
A dataset field's sub-shape is resolved and enforced.

`required` exists only on dataset fields, where it is enforced on record
create. A type property has no `required` rule.

**Pinned** paths are immutable for the definition's life. On a property:
`id`, `kind`, `items`, `properties`, `scope` (`400 property.immutable`).
On a dataset field: the whole behavioral declaration — `key`, `kind`,
`shape`, `scope`, `required`, `mutableBy`, `stamp`
(`400 dataset.immutable`). Mutable on both: `name`, `description` and
every path under a top-level `xFormat`; on a property also `xKey` and
`meta.index`. A nested descriptor under `properties` / `items` is pinned
with its parent, `xFormat` included (see Composites).

Nothing in `xFormat` is enforced by the SDK. `any` validates **every
property write** against the current slug's shape — a compound's
fields, an `any://` URI, a midnight-UTC day — and rejects what does not
fit. That is a write-boundary check, never a promise about what is
stored: values arrive by other routes and the slug can change after the
fact.

## The descriptor

### Type property

```json
{
  "id":    "YtNwvNzm9TY",
  "xKey":  "trip_dates",
  "name":  "Trip dates",
  "description": "…",

  "kind":  "object",
  "scope": "synced",

  "meta": { "index": "basic" },

  "xFormat": { … }
}
```

| field | mutable | role |
|---|---|---|
| `id` | pinned | content-addressed record id. Values live at `record[typeId][id]`, so this is the storage key. |
| `xKey` | **mutable** | the handle. An alias, not a storage key — renaming rewrites no data. Unique **within one type**; see Handles below. |
| `kind` | **pinned** | `string` · `number` · `boolean` · `array` · `object` · `datetime`. Required on create (`400 request.schema` when absent) — nothing is defaulted from the descriptor. (`null` is accepted but has no descriptor use.) |
| `items` / `properties` | **pinned** | recursive sub-shape — `items` on `array`, `properties` on `object`. Declarative on properties, and not settable over HTTP; enforced on dataset fields, which declare them as `shape` (`{kind, items?, properties?}`). |
| `scope` | **pinned** | `synced` · `account` · `local`. On dataset fields, `account` is declarable but not writable. |
| `name` / `description` | mutable | display |
| `meta` | mutable | consumer flags — `meta.index` (search scope) only; any other key is `400 request.invalid_field`. Descriptive keys live in `xFormat`. |
| `xFormat` | mutable | everything descriptive |

### Dataset field

Identical descriptor plus the record-write rules:

```json
{
  "key":  "wateredAt",
  "name": "Watered at",
  "description": "…",

  "kind":  "datetime",
  "scope": "synced",

  "required":  true,
  "mutableBy": "author",

  "xFormat": { … }
}
```

`key` is pinned and **is** the record field name — a record reads
`{"wateredAt": …}`. Properties are content-addressed instead because
their values share one row with every other type's values. A field's
`shape` (`{kind, items?, properties?}`) reads back whole; `name`,
`description` and `xFormat` mutate through
`PATCH …/datasets/:defId/fields/:fieldId`.

A stamped field (`"stamp": "creator" | "createTime" | "modifyTime"`) is
written by the handler: no `required`, no `mutableBy`, and
`mutableBy: "author"` anywhere in the dataset needs a `creator` stamp.
Its kind is the one the stamp implies (creator ⇒ string, times ⇒
datetime), and its slug is checked against that.

## `xFormat`

```json
"xFormat": {
  "type":     "choice",
  "icon":     "tag",
  "pos":      "a6",
  "options":  { "<key>": { "name": …, "color": …, "pos": …, "meta": {} } },
  "relation": { "targetTypes": ["…"], "filter": "<json text>" },
  "config":   { "<per-format>": … }
}
```

| key | applies to | notes |
|---|---|---|
| `type` | all | the semantic slug. **Open set** — an unknown value is never an error. |
| `icon` | all | glyph name |
| `pos` | all | lexid ordering key |
| `options` | enumerated formats | the map **key is the stored value**; the entry is the display slice `{name, color, pos, meta.<k>}`, all strings. `color` is an open string — clients render the palette names they know |
| `relation` | reference formats | `targetTypes` names types by type xKey (array of strings). `filter` is an additional condition over candidate objects, one JSON-text leaf that must parse as a query condition. |
| `config` | per format | scalar settings (string / number / boolean), keyed by the vocabulary below |
| `links` | any | the link-index marker: `link` (the string value is one `any://` reference, kind string), `links` (the array lists references, kind array), `markdown` (the text is scanned for references, kind string) or `none` (never scanned — the off-switch for a `relation` or `markdown` field whose references must stay out of backlinks). Implied by the `relation` slug (`links`) and the `markdown` slug (`markdown`); set it explicitly on any other shape that carries references. See docs/13-index.md § Links. |

Reserved: `validate` and `compute`. Both answer
`400 property.format_invalid` on create and on a PATCH set.

These seven are the keys `any` interprets — their leaves are typed on
write. **Any other top-level key is a vendor namespace** (`acme`),
stored verbatim at any depth, never validated. Clients preserve keys
they do not recognise.

Two conventions hold everywhere in the bag, vendor subtrees included: a
key is non-empty, contains no `.` (PATCH paths split on it, so a dotted
key could never be patched or unset on its own) and does not start with
`$` (the extended-JSON namespace — `{"$date": …}` would convert to an
instant on the way in). `"xFormat": null` on create reads as absent.

### Where a member goes

`xFormat` members are ordinary CRDT paths and follow the SDK's
documented per-path LWW. That makes one choice per member: a nested
object whose keys are edited independently, or a single leaf that
replaces whole.

> Independent traits merge. Interdependent constraints replace.

`options`, `options.<key>`, `relation` and `config` are nested — two
authors adding two options, or setting a colour and a position, both
land. `relation.filter` is a **single JSON-text leaf**, because a
field-merged query condition is not a valid query. `targetTypes` is one
array leaf.

Two consequences to expect rather than treat as bugs: an option entry is
**not atomic**, so two authors adding the same option key produce one
entry with a name from each; and `relation.filter` may outlive the
`targetTypes` it was written for — a filter naming an unknown property
is inactive, not an error.

A setting whose parts must change together is one JSON-text leaf, never
a nested object.

### Editing: leaves only

The server enforces the merge model structurally, on both surfaces: a
PATCH `set` **never carries an object**. A set targets a leaf — a
string, number, boolean or array — and a container (`xFormat` itself,
`options`, `options.<key>`, `relation`, `config`) can only be **unset**.
That is what keeps one client from writing a string over a whole option
map, or replacing the bag and dropping keys another client added; it
needs no understanding of what the members mean. Unsetting the last
option leaves an empty `options` object behind — a per-path unset never
removes the parent. The reserved `validate` / `compute` keys refuse a
set but accept an unset, so a key that arrived by another route can be
repaired.

## v1 vocabulary

`type` is open; these ship as the documented set, and `any` checks each
against the pinned `kind` on create and on a slug change. An
unrecognised slug renders structurally from `kind` and gets no value
checks. Unprefixed slugs are `any`-owned; third parties use a vendor
prefix, and own their `config` keys.

| `type` | `kind` | `options` | `relation` | `config` | value check |
|---|---|---|---|---|---|
| `text` | string | | | | string |
| `longtext` | string | | | | string |
| `markdown` | string | | | | string — inline markdown; the link index scans it for `any://` references (chat and editor `text` carry it) |
| `url` | string | | | | absolute URL with a scheme |
| `email` | string | | | | one `local@domain` |
| `phone` | string | | | | string |
| `choice` | array | ✓ | | `multiple` `optionSort` | non-empty option keys; one unless `multiple` |
| `relation` | array | | ✓ | `multiple` | bare `any://<objectId>` URIs; one unless `multiple` |
| `number` | number | | | `decimals` `separators` `showAs` `outOf` `color` | number |
| `currency` | number | | | + `currency` | number |
| `percent` | number | | | `decimals` `showAs` | number |
| `rating` | number | | | `max` `glyph` | number, `0 ≤ v ≤ max` when `max` is set |
| `checkbox` | boolean | | | | boolean |
| `date` | datetime | | | `datePattern` | instant at midnight UTC |
| `datetime` | datetime | | | `datePattern` `timePattern` `zone` | instant |
| `duration` | number | | | `unit` | number |
| `period` | object | | | | `{from?, to?}` instants, at least one, `to ≥ from` |
| `money` | object | | | | `{amount: number, currency: string}` exactly |
| `geo` | object | | | | `{lat, lng}` in range exactly |

`tags` is reserved: as a slug it is `400 property.format_invalid`.

The server types `config` leaves only as scalars; of the keys above,
only `multiple` and `max` affect its value checks. `datePattern` /
`timePattern` / `zone` have no defined value vocabulary; `optionSort`
values are open (`manual` shown).

### One or many

`choice` and `relation` store **an array, always** — a single-valued
Choice holds `["qualified"]`, not `"qualified"`. Arity is
`config.multiple`, a mutable flag, not a kind; **absent means `false`**
— one value, and a two-element write is refused.

A property that may ever hold several values has to be an array from
creation: a scalar cannot be widened to a list in place, and `kind` is
pinned.

Turning `multiple` off keeps every stored value. A client shows the
first with a count and writes a one-element array on the next edit;
never drop the rest silently.

### Dates

Both date slugs are `kind: datetime` — an instant,
`{"$date": "<RFC 3339>"}` in both directions (writes also accept
`{"$date": <unix millis>}`). Date operators (`$year`, `$dateTrunc`,
`$dateDiff`) therefore work on every date property. A date slug on a
`string` kind is `400 property.format_invalid`.

`date` is a **calendar day**, encoded as midnight UTC. **Format it in
UTC, never in local time** — a local-time render shows the previous day
for any viewer west of UTC, so a birthday or a deadline reads wrong.
`datetime` is an absolute instant and renders in the viewer's zone.

## Examples

```json
// email
{ "kind": "string", "xKey": "work_email", "name": "Work email",
  "xFormat": { "type": "email", "icon": "envelope", "pos": "a2" } }

// choice, single-valued
{ "kind": "array", "xKey": "stage", "name": "Stage",
  "xFormat": {
    "type": "choice", "pos": "a0",
    "config": { "multiple": false },
    "options": {
      "lead":      { "name": "Lead",      "color": "grey",  "pos": "a0" },
      "qualified": { "name": "Qualified", "color": "blue",  "pos": "a1" },
      "won":       { "name": "Won",       "color": "green", "pos": "a2" } } } }
// value: ["qualified"]   — an array even when single-valued

// choice, many
{ "kind": "array", "xKey": "category", "name": "Category",
  "xFormat": {
    "type": "choice", "icon": "tag", "pos": "a6",
    "config": { "multiple": true, "optionSort": "manual" },
    "options": { "weather": { "name": "Weather", "color": "green", "pos": "b09" } } } }
// value: ["weather", "pilot_error"]

// relation, single-valued, restricted by type
{ "kind": "array", "xKey": "company", "name": "Company",
  "xFormat": {
    "type": "relation", "icon": "building", "pos": "a1",
    "config": { "multiple": false },
    "relation": { "targetTypes": ["companies"] } } }
// value: ["any://bafyreihfnly3l6ceiio7xpoc47mv6pqsxoexg5z4iumtfvq3ov4ctv5ggq"]

// relation, many, restricted by type and query
{ "kind": "array", "xKey": "aircraft", "name": "Aircraft involved",
  "xFormat": {
    "type": "relation", "pos": "a2",
    "config": { "multiple": true },
    "relation": {
      "targetTypes": ["aircraft_type"],
      "filter": "{\"<typeId>.<propId>\":true}" } } }
// filter paths are resolved ids, never xKeys — see below

// currency
{ "kind": "number", "xKey": "deal_value", "name": "Deal value",
  "xFormat": {
    "type": "currency", "pos": "a3",
    "config": { "currency": "USD", "decimals": 0,
                "showAs": "bar", "outOf": 100000, "color": "green" } } }

// period — composite. See Composites below before using one on a property.
{ "kind": "object", "xKey": "trip", "name": "Trip dates",
  "xFormat": { "type": "period", "icon": "calendar" } }
// value: {"from": {"$date":"2026-09-01T00:00:00.000Z"},
//         "to":   {"$date":"2026-09-14T00:00:00.000Z"}}
```

Link values are the bare in-space form `any://<objectId>` — no space
segment, no fragment.

`relation.filter` is stored on the server, so its paths are
`<typeId>.<propId>` — resolve xKeys before writing one. `targetTypes`
names types by xKey so a declaration survives installation into another
space.

**A filter does not survive one.** Property ids are content-addressed
and differ per space, so a `filter` shipped in a bundle is inert on
arrival. Only `targetTypes` travels; a bundle should ship the type
restriction and leave the filter to the installing space.

**`targetTypes` can be ambiguous.** Type xKey is not convergently
unique — the uniqueness check (`409 type.xkey_conflict`) is a
read-then-create preflight — so two types created apart can share a
handle, and a reference naming it resolves to either.

## Composites

> A compound is **one value, written whole**. Its parts are not
> independently editable — that is the intended semantics, not a
> limitation. If the parts *are* edited independently, they belong in
> separate properties, not a compound.

`period`, `money` and `geo` all satisfy that test. A date range is
picked in one interaction, an amount and its currency change together,
a pin's two coordinates are one location.

Write the whole object:

```json
POST …/properties/:objectId/set/:typeId
{ "patch": { "<propId>": { "from": {"$date":"2026-08-14T00:00:00.000Z"},
                           "to":   {"$date":"2026-08-18T00:00:00.000Z"} } } }
```

`/set` keys a value by propId with no sub-paths, so this is one `$set` of
the whole value and it **replaces** what was there. Two useful
consequences: an open-ended value is the same write with the key omitted
(`{"from": …}` clears `to` — `/set` has no sub-path unset), and
cross-part invariants like *end on or after start* are enforceable at
write time, because one client writes both parts in one op — and `any`
enforces exactly those (§ v1 vocabulary).

Concurrent whole-value writes resolve last-writer-wins — the right rule
for one value, and the reason to prefer a compound over two linked
properties, which can merge into a combination neither author wrote.

Two limits to know. Object-kind values are **not search-indexed** (the
property chunker covers string, array, number and datetime — an object
"carries no discoverable text"), which is irrelevant for a range or an
amount and would matter for a compound carrying text. And nested
descriptors are **pinned wholesale** — `properties` is a pinned path,
so a sub-field's `name`, `icon` and `xFormat` cannot be edited after
creation. Declare them correctly, or mint a new property.

A compound's `kind` is `object`, so a `date` cannot become a `period`:
that is a new property, exactly as Text cannot become Number. Offer
"Date range" as its own entry in the picker rather than an "add an end"
toggle.

A range is an object rather than an array of instants because the date
operators need a scalar: `$dateTrunc` and `$year` over an array return
**null**, so a date column could not be grouped by day, week, month or
year, and array matching is per element, so "due after X" would match
any range whose *end* is after X.

## Client rules

### Rendering

```
1. slug known AND value fits it   → render as that format
2. slug known, value doesn't fit  → render structurally by kind; may flag
3. slug unknown                   → render structurally by kind
4. always                         → never drop, never crash, never rewrite
```

Rule 4 is the one that costs data when skipped. **Read-tolerance without
write-preservation destroys data**: a table that reads `["a","b"]` as
links, fails to parse them, and writes `[]` back on save has silently
deleted the column. Tolerating a value means passing it through
untouched.

The write side is **strict**: a value that does not fit the current slug
is rejected (`400 property.format_violation`), including an unchanged
re-save of one that was stranded by a slug flip or written through the
raw `/modify` route. The rule for a client is therefore *never silently
rewrite* — refusing to save and surfacing the error is correct;
normalising the value to make it pass is not.

Edit `xFormat` leaf by leaf (§ Editing: leaves only).

### Values a client must tolerate

1. Value does not match the slug — the slug changed after the values were written
2. Option key absent from `options` — deleted, or the value predates it
3. Unknown slug, or an unknown key inside `xFormat`
4. Link target missing, deleted, or outside `relation.targetTypes`
5. A value under a propId with no definition — the definition was removed
6. No `xFormat` at all — an agent declared only `kind`
7. `config` value of an unexpected type

None are errors. All render structurally.

### `type` is mutable, and `kind` bounds the damage

Changing the slug can strand existing values — they still read, but
cannot be re-saved until corrected or the slug is reverted — so a client
offering the change should say so. `kind` is pinned, and the server
refuses a slug that does not fit it, so the slug moves only **within
one kind**:

| `kind` | moves between | effect |
|---|---|---|
| `string` | text · longtext · markdown · url · email · phone | harmless — the values are all lines of text |
| `number` | number · currency · percent · rating · duration | harmless, display only |
| `datetime` | date · datetime | changes the render zone; see Dates |
| `object` | period · money · geo | shape is pinned, so values survive an incoherent slug |
| `array` | choice ↔ relation | option keys sit where `any://` URIs are expected |

`choice ↔ relation` is the one lossy-looking move — both are arrays of
strings, so it is structurally legal. Clients should not offer it.

Reverting the slug restores the display of values written before the
flip — nothing rewrote them. It does not undo values written *under*
the new slug: flip a Choice to a Relation, let someone pick in the cell,
and `any://` URIs now sit in a field whose options are keys.

### Ordering

Sort by `xFormat.pos` (plain byte-wise compare), then by `id` / `key`.
`pos` is mutable — a re-rank writes it — so the immutable `id` tie-break
is what keeps the order stable. Do **not** tie-break on `name` — it is
mutable, so an unrelated rename would reorder the list. Server response
order is unspecified.

Two properties can share a `pos`: the lexid is client-generated and two
devices dropping into the same gap compute the same key. The `id`
tie-break keeps that deterministic; re-rank later if it matters. A
re-rank writes only the leaves it moves.

### Handles

`xKey` is unique **within one type**, checked on add and on rename
(`409 property.xkey_conflict`). The check is a convenience, not a
guarantee: it is a read-then-create preflight, so two devices working
apart can both land the same handle.

When that happens the two definitions have different `id`s and **both
columns persist** — each device's values are intact and correctly typed
under their own propId. They do not merge. This is the defined
behaviour, and resolving it is the client's job: surface both, and let
the user rename or remove one. A handle is not an address for anything
stored — filters, sorts and saved views all reference
`<typeId>.<propId>`.

Uniqueness is per type, so an object carrying two types may hold two
properties with the same handle. Resolve a handle within a known type;
there is no object-wide handle namespace.

### Presentation tiers

Three homes, chosen by who a change affects:

| tier | home | example |
|---|---|---|
| inherent to the data | `xFormat` | this number is USD; this option is red |
| shared framing | saved view `layoutSettings` | in *this* view, hide column X, width 320 |
| per-viewer | the client's own account settings or `/v1/local` | *I* read dates as DD/MM, 24h, Europe/Berlin |

Nothing schema-shaped belongs in browser storage. Column widths belong
to the view — the same property is 320px in one table and 120px in
another. Per-viewer preferences are one setting per viewer, not one per
property, and they are not a `scope` on the definition: `scope` is a
value write route, not a home for definition metadata.

## What the server enforces

Everything the SDK does not: the SDK stores `x-format` as one object
created whole, lets every path under it mutate, and enforces `kind`
alone.

- **On create** (`POST …/properties`, bundle `properties`, dataset
  field drafts): the seven interpreted keys are typed, the slug and the
  `links` marker are checked against the pinned `kind`, `tags` /
  `validate` / `compute` are refused, vendor keys pass verbatim.
  `400 request.invalid_field` for a shape problem,
  `400 property.format_invalid` for a vocabulary one.
- **`xKey` uniqueness** within the type on add and on rename —
  `409 property.xkey_conflict`.
- **On PATCH** (properties and dataset fields): the leaf-only rule; the
  same leaf typing; a slug or `links` move checked against the kind;
  pinned paths `400 property.immutable` / `400 dataset.immutable`.
- **On every property write** (`/set`, object-create `initialProperties`,
  bundle `rootProperties`): the value against the current slug —
  `400 property.format_violation` with `details.{propId, format, reason}`.
  Raw `POST …/modify` on the `objects` dataset is not checked.
- **The link index** reads references off every property and field
  whose top-level descriptor carries a `links` marker, implied or
  explicit. A `relation` slug or `links` marker nested in a composite is
  legal but invisible to it.
- `meta` narrowed to `index`.

## Not covered

Named so clients do not model them as `xFormat` extensions without a
contract:

- **File / image values.** A file is `any://f/<spaceId>/<fileId>` — a
  different URI kind, and files are not objects, so
  `relation.targetTypes` has nothing to bind to. No slug covers them.
- **Person / identity values.** A `relation` pointing at objects of a
  user-defined "Person" type is an ordinary object reference. Pointing
  at a *space member* is not supported: identities are not objects,
  `any://m/<spaceId>/<identity>` exists for mentions but is not wired to
  `relation`, and `targetTypes` has no way to name them. An "Assignee"
  that means a member rather than a contact record is unsupported.
- **Computed values** — formula, rollup, lookup. Not expressible: a
  handler may read only its own object's datasets, and only fields
  immutable post-create, so a stored derived value cannot depend on an
  edited one. `compute` is reserved.
- **Declarative assertions.** `validate` is reserved: a third-party or
  agent-authored format renders correctly but enforces nothing beyond
  `kind` and the vocabulary checks above.
- **Paired / inverse relations, cardinality.** Declarable as advisory
  keys; maintaining the pair means writing a second object, which no
  handler can do.
- **Localisation of `name` / `description` / option labels.**
- **Autonumber, non-date ranges, unit/measure properties.**
- **Slugs for built-in system values.** Every field served through
  discovery or a built-in type's properties — the module and static
  datasets (`chat_messages`, `editor_blocks`, `dataviews` / `views`),
  the `objects` row's `any.*` properties, the built-in types' properties
  and the SDK's system datasets — carries a `description`, and an
  `xFormat` where the vocabulary above names its value (`text`,
  `longtext`, `markdown`, `datetime`, `checkbox`). Two value shapes
  have no slug and ship description-only: account identities (the
  "Person / identity values" item above) and record ids
  (`replyToMessageId`, `views.dataview`). Icon values, lexid `pos`, enum
  strings and opaque objects are system values and carry no slug.
