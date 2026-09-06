# Well-known bundles — the usecase catalog (client contract)

The server ships a catalog of well-known bundles, grouped into
**usecases** a client sets up in a space with one call, dependencies
included. Every client, device and member that sets a usecase up lands
on the same types with the same ids. The catalog is a yaml file
embedded in the binary (`internal/catalog/catalog.yml`), read-only over
HTTP, validated at build time; the registry mechanics underneath are
the ordinary bundles surface (`03-api.md` § Bundles). Nothing is set up
unless a client asks.

## Model

```
usecase  = { id, name, description?, requires: [usecase id], bundles: [bundle] }
bundle   = { id: "system:<name>/v<n>", name, description?, derived?, hidden?, type?, miniapp?, parts? }
type     = { xKey, weight?, layout?, properties: [property draft with xKey] }
miniapp  = { bundle?, <any other property of the built-in miniapp type> }
parts    = [ part draft ]                     # the POST …/types/:typeId/parts shape
```

A **usecase** is the unit a client asks for: a set of bundles installed
together plus `requires`, the usecases that must be present first.
A **bundle** is one root object registered under a permanent
`system:` id — one registry row, one root, for the space's lifetime.
It declares at least one of `type`, `miniapp`, `parts`, and what the
root IS follows from that:

| declares | the root is | objects relate to it by |
|---|---|---|
| `type` | a type object, `typeId = rootId`; `xKey` is its handle | carrying it in `any.types`; values at `<typeId>.<propId>` |
| `miniapp` | the object a client opens; carries the built-in `miniapp` with `bundle` = the bundle id | opening it; `miniapp.bundle` says what to run |
| `parts` | a records host — the app's own state lives in records on the root | nothing; a records host asks for `hidden` |
| `type` + `miniapp` | one object that is both (the wiki: the app, and the type its pages carry) | both |

`type: {xKey}` alone — no properties, no parts — is a valid **marker
type**: a flag objects carry, resolvable by handle, with no columns.

**Created roots, one exception.** Every catalog root is a created
root: deletable (`DELETE …/objects/:rootId` uninstalls it), forking
when two devices of one account install while apart (§ Forks). The
general chat is the one `derived: true` bundle — chat content cannot
be merged across objects (`creator` / `createdAt` come from the change
envelope), and a 1-1 space has no owner to break a tie — so its root
is a function of (space, bundle id): it never forks and can never be
deleted.

**Naming.** Usecase ids are slugs (`[a-z][a-z0-9-]*`: `wiki`,
`general-chat`, `contacts`); they never enter a space and need no
encoding in the path. Bundle ids are `system:<name>/v<n>` — the
`system:` prefix makes them the server's (a client ensure under it is
`409 bundle.reserved`), the version is part of the id, and a bundle
that changes shape takes a new id (§ Evolution). Type xKeys
(`[a-z][a-z0-9_]*`) are unique across the catalog and disjoint from
the registered type ids; property xKeys are unique within their type.
`relation.targetTypes` name types by xKey, so cross-bundle references
need no id resolution.

## Endpoints

Account-scoped, behind the auth guard, outside the space group
(the catalog belongs to the server, not to a space).

```
GET  /v1/catalog                    → 200 {usecases: [CatalogUsecase]}
GET  /v1/catalog/:usecaseId         → 200 CatalogUsecase            404 catalog.not_found
POST /v1/catalog/:usecaseId/setup   → 200 CatalogSetupResponse
     {spaceId}
```

`CatalogUsecase` is the entry as the catalog declares it:

```jsonc
{ "id": "wiki", "name": "Wiki", "description": "A tree of pages — parent link, sibling order and folder flag",
  "bundles": [ { "id": "system:wiki/v1", "name": "Wiki", "hidden": true,
                 "miniapp": { "bundle": "system:wiki/v1" },
                 "type": { "xKey": "wiki",
                           "properties": [ { "xKey": "parentId", "name": "Parent",   "kind": "string",  "meta": { "index": "none" } },
                                           { "xKey": "pos",      "name": "Position", "kind": "string",  "meta": { "index": "none" } },
                                           { "xKey": "folder",   "name": "Folder",   "kind": "boolean", "xFormat": { "type": "checkbox" } } ] } } ] }
```

`requires` is present when non-empty; `type.weight` / `type.layout`,
`derived`, `hidden` and `parts` when set. Property and part entries
are the `POST …/types/:typeId/properties` / `…/parts` draft shapes.

`CatalogSetupResponse` lists every bundle the call touched,
dependencies first, the requested usecase's bundles last:

```jsonc
{ "usecase": "contact",
  "bundles": [
    { "usecase": "people", "id": "system:person/v1",
      "bundle": { "id": "system:person/v1", "name": "Person", "rootId": "<rootId>", "roots": ["<rootId>"] },
      "installed": true, "typeId": "<rootId>",
      "properties": { "email": "<propId>", "phone": "<propId>", "organization": "<propId>", "…": "…" } },
    { "usecase": "people", "id": "system:organization/v1", "…": "…" },
    { "usecase": "contact", "id": "system:contact/v1",
      "bundle": { "…": "…" }, "installed": true, "typeId": "<rootId>",
      "properties": { "owner": "<propId>", "status": "<propId>", "…": "…" } } ] }
```

Per bundle: `usecase` (the entry it belongs to — the requested one or
a dependency), `id`, `bundle` (the converged registry row, the
`03-api.md` § Bundles shape), `installed` (THIS call registered the
root; for the derived chat, what this device did), and when the bundle
declares a type — `type`, or `parts` (a records host) — `typeId` (the
root id) and `properties`, every property the root carries with an
xKey mapped to its id (absent when there are none). A `miniapp`
bundle echoes `miniapp`: the value map the catalog declares, `bundle`
filled in.

Errors: `404 catalog.not_found` (`details.usecaseId`);
`400 request.missing_field` without `spaceId`; `405 space.unsupported`
on the tech space; the space errors (`404 space.not_found`, …); and
per bundle the `03-api.md` § Bundles set — `409 bundle.not_ready`,
`403` for a member without write permission on an install path,
`409 type.xkey_conflict` (§ Handles). Every error of the walk carries
`details.usecaseId` (the id asked for), `details.usecase` and
`details.bundleId` (the step that failed) and `details.spaceId`.

CLI: `any catalog list`, `any catalog get <usecaseId>`,
`any catalog setup <usecaseId> <spaceId>` (`01-cli.md` § Catalog).

## Setup

One call does, in order:

1. **Dependency closure.** The usecase's transitive `requires`,
   dependencies first, each once, the requested usecase last;
   `requires` are walked in declaration order, so the order is
   deterministic. Within a usecase, bundles in declaration order.
2. **One registry-convergence wait** for the whole list — the same
   wait a single ensure runs (30s with a peer connected, 3s with none;
   instant when already synced). The first bundle that needs the
   verdict pays it, the rest reuse it.
3. **Adopt-or-install per bundle.** A live winner is adopted — a pure
   read, so a reader member gets the ids too; a writer's adopt also
   heals what the root lacks (§ Evolution). Otherwise the server checks
   the handle (§ Handles), creates the root with everything the bundle
   declares — the type marker plus its own id, the `miniapp` type with
   its values, the parts and properties, name, xKey, weight, layout,
   hidden — and registers it. A created root lands as root + 3
   changes: one `objects` change with the types, the name, the type
   metadata and the seeded `miniapp` values, one `properties` change,
   one `datasets` change; a peer may briefly see the definitions
   before the parts.

Every step is idempotent: a second setup adopts everything
(`installed: false`, same ids). A **failure mid-walk** leaves the
dependencies it installed, names the failing step in
`details.usecase` / `details.bundleId`, and the next call resumes from
the registry's state.

Who may do what: when the wait cannot complete, the space's **owner**
installs anyway (only this account's own devices could compete, and
the registry converges those); any other member is
`409 bundle.not_ready` and retries when the network is back — so a
space set up by its owner before sharing spares joiners the wait. A
member without write permission adopts but cannot install (`403` on
the install path). The create-and-register section runs detached from
the request (shutdown-bounded), so a client that disconnects mid-walk
cannot leave an orphan root.

Which usecases a space has is read off the space's bundle list — every
member row is there under its `system:` id (`GET …/bundles`, locked on
convergence, `synced` says whether absence is definitive) — mapped to
usecases through the catalog. The reply of a setup is the convenient
form of the same information.

## Handles

The catalog's type xKeys are checked against the space **on the
install path only**: before a root is minted, no type in the space —
hidden or not, user or registered — may hold the bundle's `xKey` (as
its `xKey` or as its id). A hit is `409 type.xkey_conflict` with
`details.xKey` and `details.existingTypeId`, raised before anything is
created, so a refused setup leaves nothing behind (the dependencies
before it stand). An adopted install carries the handle by design and
is never a conflict with itself; the registered built-ins are checked
against the catalog at build time, so they cannot collide at setup.

The case that bites: a user type minted before the catalog knew the
handle (`contact`, `person`, …). It blocks that usecase in that space
until the type is gone. There is no rename or delete of a type over
HTTP in v1, so this is an open item (`07-roadmap.md`); until then a
client sets the usecase up before offering a type picker, or steers
its own handles away from the catalog's.

## What a client does with the reply

1. Read the catalog to offer usecases; read the space's bundles to see
   what is set up.
2. `POST /v1/catalog/<usecase>/setup` once per space per usecase (and
   again after a failure, or after a release that evolved the usecase),
   and keep the reply: per type, `typeId` and the xKey → propId map.
3. Create objects with `types: [<typeId>]`, write values under
   `<typeId>.<propId>`, query with `{"<typeId>.<propId>": …}` and
   `{"any.types": <typeId>}`. Never key by xKey on the wire.
4. Open a miniapp root by reading `miniapp.bundle` on it and running
   what that bundle id means to the client; the app's own state is the
   records on that root (a `parts` bundle's collections are
   `<rootId>_<key>`, read off `GET …/types/<rootId>/parts`).
5. **Resolve catalog types through the bundle registry** — bundle id →
   `rootId` — never by scanning the type list for the xKey: after a
   fork two roots carry the same xKey until the loser is resolved, and
   the registry names the winner. `relation.targetTypes` are type
   xKeys: for a catalog xKey go through the registry the same way; for
   a user type's xKey the type list is the only source.
6. Watch the space's bundles (`POST …/query[/subscribe]` on the
   `bundles` dataset of `spaceIndexObjectId`) for `losers` and run
   § Forks when one appears.

## Rendering

- The primary type of an object is the **listed** carried type with
  the highest `weight`; its `layout` renders. A **hidden** type never
  competes for the primary type and takes no weight (the catalog
  refuses `weight` next to `hidden`).
- Parts and properties of a hidden type still render: a hidden type
  contributes its columns and its parts, only never the layout.
- A hidden **self-typed root** that is itself the rendered object — the
  general chat — renders by its own layout (`{"type": "chat"}`),
  because nothing else is carried.
- Then every carried type's parts in `pos` order: a `records` part is
  a table over `<typeId>_<key>` on the object, an `editor` part the
  body (shared with `page`), a `chat` part a chat.
- A type picker lists the catalog's listed types like any user type and
  hides types the registry reports as losers.

## Forks

Every catalog root except the chat is a created root, so two devices
of one account that set a usecase up while apart each mint a root —
two `person` types with one xKey. After sync the registry names one
winner and lists the other in `losers`; objects that carried the loser
keep their values under the loser's id. The client that observes a
loser re-homes those objects onto the winner **column by column
through the xKey map**: attach the winner type, copy each value from
`<loserId>.<propId>` to the winner's property of the same xKey, detach
the loser; merge a records host's records the same way (upsert into
the winner's collection); then `POST …/bundles/system%3A<name>%2Fv1/resolve`
with `{loserRootId}` — the server deletes the loser once its tree has
settled (`409 bundle.loser_not_ready` until then, retried in the
background). The server never merges: only the client knows what the
values mean. The bundle id carries a slash, so the path segment is
percent-encoded.

Deriving every root instead was rejected: a derived root can never be
deleted, which would make every catalog type and app uninstallable.

## Evolution

Adopt heals what a root lacks and never patches, so a later release
may, without a new bundle id:

| change in the catalog | lands on existing installs |
|---|---|
| a property added to a type | at the next setup by a writer: written by handle under its deterministic id; a definition the root carries (live, or removed — the tombstone keeps the id) is left alone |
| a `miniapp` value added | at the next setup by a writer: written where absent, never overwritten; the built-in `miniapp` type is attached first when the root predates it |
| an option key added to a `choice` property | at the next setup by a writer: the key is written with its catalog leaves (`name`, `color`, `pos`); a key the definition already carries is left exactly as the space has it — renamed, recoloured, reordered or not |
| a bundle added to a usecase, a usecase added to `requires` | at the next setup: installed like any other step |

Everything else on an existing install — an option's leaves once
present, a removed option, other `xFormat` edits, display `name`,
`weight`, `layout` and `hidden` — is the space's to change after
install (client `PATCH` calls on the space's copy); the catalog never
patches or removes what a root carries.

What needs a **new bundle id** (`/v2`, a new root; the old install
stays and the client migrates content): changing or removing a part or
a dataset (declared once, all-or-nothing), changing a property's
`kind` or `scope` (pinned), renaming a type or property xKey (the
property id derives from it), changing `derived`.

Uninstall is `DELETE …/objects/<rootId>`: the id then reads as not
installed and a later setup mints a fresh root. There is no
usecase-level uninstall and no reference counting — a dependency stays
until its root is deleted, and deleting a type other objects carry
leaves their values orphaned (readable, no schema).

## Shipped usecases

| usecase | requires | bundle | declares |
|---|---|---|---|
| `wiki` | — | `system:wiki/v1` | type `wiki` (hidden; `parentId`, `pos` — not indexed — and `folder`, a checkbox) + miniapp |
| `collections` | — | `system:collections/v1` | miniapp only — a feature switch: installing it turns the types feature on in clients |
| `general-chat` | — | `system:general-chat/v1` | derived, hidden; type `general_chat` with `layout {type: chat}` and one shared `chat` part |
| `people` | — | `system:person/v1` | type `person` (weight 10, layout `profile`; email, phone, organization → `organization`, job_title, location, linkedin, birthday, tags) + shared editor `body` part |
| | | `system:organization/v1` | type `organization` (weight 10, layout `profile`; kind, domain, categories, location, size, linkedin, main_contact → `person`) + shared editor `body` part |
| `contact` | `people` | `system:contact/v1` | type `contact` (weight 5; owner → `person`, status, source, referred_by → `person`, last_contact, next_follow_up) — the relationship facet on a person or organisation |
| `investor` | `people` | `system:investor/v1` | type `investor` (weight 5; investor_type, investor_status, focus, stages, check_size, portfolio → `organization`) |
| `customer` | `people` | `system:customer/v1` | type `customer` (weight 5; account_status, plan, annual_value, customer_since, renewal_date) |
| `partner` | `people` | `system:partner/v1` | type `partner` (weight 5; partnership_type, partner_status, since, review_date) |
| `vendor` | `people` | `system:vendor/v1` | type `vendor` (weight 5; services, vendor_status, contract_value, renewal_date) |
| `cofounder` | `people` | `system:cofounder/v1` | type `cofounder` (weight 5; founded → `organization`, since, responsibilities, equity) |
| `candidate` | `people` | `system:candidate/v1` | type `candidate` (weight 5; role, candidate_stage, next_interview, resume) |
| `contacts` | `people`, `contact` | `system:contacts/v1` | miniapp, hidden; records part `layouts` (dataset `layouts`, `idRule: user` — the id is an identity type's xKey; field `blocks`, array) |
| `crm` | `contacts` | `system:deal/v1` | type `deal` (weight 10, layout `profile`; stage, owner → `person`, organization → `organization`, amount, close_date) + shared editor `body` part |
| | | `system:crm/v1` | miniapp only |

Thirteen usecases, fifteen bundles, twelve types. The two identities
are one usecase because `person.organization` and
`organization.main_contact` reference each other and the `requires`
graph must stay acyclic; the roles are one usecase each so a role
lands only when picked (`crm` does not require them); `deal` lives in
`crm` because only the CRM needs it. `choice` and `relation`
properties are `kind: array` even when single-valued; an amount is
`currency` on `number`. The yaml is the authoritative declaration —
`GET /v1/catalog` returns it verbatim.

## Validation

The catalog is validated as a whole, every problem reported at once
with its yaml path, in three places that run the same code
(`server.ValidateCatalog`): at server boot (any problem refuses to
start, all of them listed), in the test suite (a broken embedded
catalog fails `make test`), and as a **build step** —
`make catalog-validate [FILES="candidate.yml …"]` checks the embedded
catalog and any candidate files, prints one
`<source>: <path>: <code>: <message>` line per problem and exits 1 on
any (`embedded catalog: ok` otherwise). CI runs it on every pull
request and before every release build (`18-ci.md`), so a broken
catalog can neither merge nor ship. Offline: no server, no space.

Problem codes of the structural layer (`internal/catalog`):

| code | meaning |
|---|---|
| `catalog.bad_yaml` | the source does not parse, or does not decode into the catalog shape |
| `catalog.unknown_field` | a key no catalog struct declares (strict decoding) |
| `catalog.bad_id` | a usecase id that is not a slug, a bundle id off `system:<name>/v<n>`, a type or property xKey or a part key off its grammar |
| `catalog.duplicate` | a usecase id, bundle id, type xKey (also when it equals a registered type id), property xKey, part key, dataset key or `requires` entry declared twice |
| `catalog.missing` | a required piece absent — usecase name or bundles, bundle name, a declaration (`type` / `miniapp` / `parts`), property xKey or kind, a dataset key, a relation's `targetTypes` |
| `catalog.bad_field` | a field that contradicts the rest — `weight` next to `hidden`, `hidden` without `type` or `parts`, `meta` beyond `index`, `relation.filter`, a wrong module, `chat` not shared, `records` shared, fields on a module dataset, `deleteBy: author` without a creator stamp |
| `catalog.unknown_usecase` | a `requires` entry naming no usecase |
| `catalog.cycle` | a self-require, or a cycle in `requires` — reported as its path (`a → b → a`) |
| `catalog.broken_link` | a `relation.targetTypes` xKey that is no type of the usecase, its transitive `requires` or a built-in; names the usecase that would have to be required when the type exists elsewhere in the catalog |
| `catalog.bad_miniapp` | `miniapp.bundle` differing from the bundle id, a key the built-in `miniapp` type lacks, or a value of the wrong kind |

The server layer adds the descriptor gate and reports its own codes
verbatim at the property's or part's path: `property.format_invalid`
(a slug on the wrong kind, a bad option shape), the part / dataset
declaration codes (`dataset.decl_invalid`, …), a layout that does not
encode. Not checked, by design: whether a space already holds a type
with a catalog handle (a runtime `409 type.xkey_conflict` at setup),
and the `derived` choice (a policy, not a syntax).

## What clients delete

The client recipe for the space's chat — registering `general-chat/v1`
with a chat part — is what the catalog's `general-chat` usecase
declares verbatim under the id `system:general-chat/v1`. The ids
differ, and a derived root's id is a function of the bundle id, so
**`POST /v1/catalog/general-chat/setup` derives a different root than
`general-chat/v1`**: a space set up under the client recipe keeps its
chat there, and setting the usecase up creates a second, empty chat.
Nothing migrates. The client recipe (`16-chat.md` § Finding the chat
object, `08-clients.md` § 4) stays the convention until the follow-up
that reserves the `chat` module to the catalog; the switch of ids
belongs to that change.

## See also

- `03-api.md` § Catalog (endpoints), § Bundles (registry, derived
  roots, bundle-declared types, resolve), § Types → Built-in hidden
  types (`miniapp`)
- `27-descriptors.md` (the `xFormat` vocabulary the catalog's
  properties use), `24-data-views.md` (views on a catalog type: a
  `dataview` on the type object)
- `16-chat.md` § Finding the chat object, `25-favorites.md` (a
  client-registered bundle, for contrast)
- `18-ci.md` § PR checks (`catalog-validate`), `07-roadmap.md`
  (uninstall / refcount, handle collisions, setup
  objects)
