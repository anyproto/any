# Well-known bundles — the usecase catalog (client contract)

The server ships a catalog of well-known bundles, grouped into
**usecases** a client sets up in a space with one call, dependencies
included. Every client, device and member that sets a usecase up lands
on the same types and collections with the same ids. The catalog is a
yaml file embedded in the binary (`internal/catalog/catalog.yml`),
read-only over HTTP, validated at build time; the registry mechanics
underneath are the ordinary bundles surface (`03-api.md` § Bundles).
Nothing is set up unless a client asks.

**The catalog is the source of truth for every well-known app, type
and collection.** An app that ships in a client — a sidebar entry, a
built-in surface, an agent's ingest target — declares in its bundle
everything it needs: the miniapp root AND the types, collections,
properties and datasets its content uses. A client never mints a type
or a collection of its own for one of these: two clients (or one
client on two devices, or an agent) minting by xKey race on `409
type.xkey_conflict` and leave a space where one device's entries are
invisible to the other. Resolve the definition from the setup reply or
the registry (§ What a client does with the reply). A client-minted
definition is one a USER creates in their own space; one a shipped app
needs belongs in `internal/catalog/catalog.yml`.

## Model

```
usecase    = { id, name, description?, requires: [usecase id], bundles: [bundle] }
bundle     = { id: "system:<name>/v<n>", name, description?, derived?, hidden?,
               type?, collection?, miniapp?, parts? }
type       = { xKey, layout?, properties: [property draft with xKey] }
collection = { xKey, properties: [property draft with xKey] }
miniapp    = { bundle?, <any other property of the built-in miniapp collection> }
parts      = [ part draft ]                   # the POST …/types/:typeId/parts shape
```

A **usecase** is the unit a client asks for: a set of bundles installed
together plus `requires`, the usecases that must be present first.
A **bundle** is one root object registered under a permanent
`system:` id — one registry row, one root, for the space's lifetime.
It declares at least one of `type`, `collection`, `miniapp`, `parts`,
and what the root IS follows from that:

| declares | the root is | objects relate to it by |
|---|---|---|
| `type` | a type object, `typeId = rootId`; `xKey` is its handle | being their one `any.type`; values at `<typeId>.<propId>` |
| `collection` | a collection object, `collectionId = rootId`; `xKey` is its handle | listing it in `any.collections`; values at `<collectionId>.<propId>` |
| `miniapp` | the object a client opens; filed under the built-in `miniapp` collection with `bundle` = the bundle id | opening it; `miniapp.bundle` says what to run |
| `parts` | a type with datasets; the records live on the objects of that type — and on the root itself | having it; records in the storage collection `<typeId>_<key>` |
| `collection` + `miniapp` | one object that is both (the wiki: the app, and the collection its pages are filed under) | both |

`type: {xKey}` alone — no properties, no parts — is a valid **marker
type**: what an object IS, resolvable by handle, with no columns.
`collection: {xKey}` alone is the same on the other surface: a marker
collection objects are filed under.

**A root defines a type or a collection, not both**, and **a
collection declares no parts** — both are refused as
`catalog.bad_field` (§ Validation). A `type` block takes `layout`; a
`collection` block has no layout and no parts, because an object's
layout and parts come from its one type. Property drafts are identical
on the two surfaces.

**A definition hosts itself.** A declaring root holds its own
`<rootId>.<propId>` values and the records of its own datasets with no
flag and no self-membership: the contacts root keeps its `layouts`
records, the general chat's root its messages. The marker sits in
`any.type` — `__type__` on a type object, `__collection__` on a
collection object, never the root's own id — so a definition row
matches neither `{"any.type": "<rootId>"}` nor
`{"any.collections": "<rootId>"}`, and a member query needs no marker
exclusion.

**Created roots, one exception.** Every catalog root is a created
root: deletable (`DELETE …/objects/:rootId` uninstalls it), forking
when two devices of one account install while apart (§ Forks). The
general chat is the one `derived: true` bundle — chat content cannot
be merged across objects (`creator` / `createdAt` come from the change
envelope), and a 1-1 space has no owner to break a tie — so its root
is a function of (space, bundle id): it never forks and can never be
deleted (`409 object.derived_undeletable`). It is also the one
declaration of the reserved `chat` module: a client part, dataset or
bundle naming `chat` is `400 dataset.module_reserved`, and setting the
general-chat type on any object other than the root is `400
type.reserved_carrier` — a space has exactly one chat (`16-chat.md`
§ Finding the chat object for a space).

**Naming.** Usecase ids are slugs (`[a-z][a-z0-9-]*`: `wiki`,
`general-chat`, `contacts`); they never enter a space and need no
encoding in the path. Bundle ids are `system:<name>/v<n>` — the
`system:` prefix makes them the server's (a client ensure under it is
`409 bundle.reserved`), the version is part of the id, and a bundle
that changes shape takes a new id (§ Evolution). Type and collection
xKeys (`[a-z][a-z0-9_]*`) share one namespace — unique across the
catalog, types and collections together, and disjoint from the
registered type ids and the meta ids `any`, `type` and `collection`;
property xKeys (`[A-Za-z][A-Za-z0-9_]*`) are unique within their
definition. `relation.targetTypes` name types by xKey, so cross-bundle
references need no id resolution.

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
                 "collection": { "xKey": "wiki",
                                 "properties": [ { "xKey": "parentId", "name": "Parent",   "kind": "string",  "meta": { "index": "none" } },
                                                 { "xKey": "pos",      "name": "Position", "kind": "string",  "meta": { "index": "none" } },
                                                 { "xKey": "folder",   "name": "Folder",   "kind": "boolean", "xFormat": { "type": "checkbox" } } ] } } ] }
```

`description` and `requires` are present when non-empty;
`type.layout`, `derived`, `hidden` and `parts` when set. Property and
part entries are the `POST …/types/:typeId/properties` / `…/parts`
draft shapes. A `miniapp` map always carries `bundle` = the bundle id,
filled in where the yaml omits it.

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
      "bundle": { "…": "…" }, "installed": true, "collectionId": "<rootId>",
      "properties": { "owner": "<propId>", "status": "<propId>", "…": "…" } } ] }
```

Per bundle: `usecase` (the entry it belongs to — the requested one or
a dependency), `id`, `bundle` (the converged registry row, the
`03-api.md` § Bundles shape), `installed` (this call registered the
root; for the derived chat, two devices can both report `true` for the
one root they share), and when the bundle declares a definition the
root id under the slot it declares — `collectionId` for a
`collection` bundle, `typeId` for a `type` or `parts` one — plus
`properties`, every property of that definition that has an xKey,
mapped to its id (absent when there are none). A `miniapp` bundle
echoes `miniapp`: the value map the catalog declares, `bundle` filled
in.

Errors: `404 catalog.not_found` (`details.usecaseId`);
`400 request.missing_field` without `spaceId`; `405 space.unsupported`
on the tech space; the space errors (`404 space.not_found`, …); and
per bundle the `03-api.md` § Bundles set — `409 bundle.not_ready`,
`403` for a member without write permission on an install path,
`409 type.xkey_conflict` (§ Handles). An error raised by a bundle step
carries `details.usecaseId` (the id asked for), `details.usecase` and
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
   declares — the marker in `any.type` (`__type__` or
   `__collection__`), the `miniapp` collection with its values, the
   parts and properties, name, xKey, layout, hidden — and registers
   it. A declaring root lands as root + up to 3 changes: one `objects`
   change carrying the marker, the root's collections, `any.name`, the
   definition metadata and the seeded `miniapp` values; then, after
   the registry row, one `datasets` change when the bundle declares
   parts and one `properties` change when it declares properties, in
   that order — a peer may briefly see the parts before the property
   definitions. A bundle with no declaration (a bare miniapp) mints
   its root through the ordinary object create plus the name stamp.

Every step is idempotent: a second setup adopts everything
(`installed: false`, same ids). A **failure mid-walk** leaves the
dependencies it installed, names the failing step in
`details.usecase` / `details.bundleId`, and the next call resumes from
the registry's state.

Who may do what: when the wait cannot complete, the space's **owner**
installs anyway (only this account's own devices could compete, and
the registry converges those), and so does every member for the
derived chat (its root cannot fork); any other member is
`409 bundle.not_ready` and retries when the network is back — so a
space set up by its owner before sharing spares joiners the wait. A
member without write permission adopts but cannot install (`403` on
the install path). The create-and-register section runs detached from
the request, bounded by shutdown and two minutes for the whole walk,
so a client that disconnects mid-walk cannot leave an orphan root.

Which usecases a space has is read off the space's bundle list — every
bundle of a set-up usecase has a row under its `system:` id
(`GET …/bundles`, locked on convergence, `synced` says whether absence
is definitive) — mapped to usecases through the catalog. The reply of
a setup is the convenient form of the same information.

## Handles

The catalog's xKeys are checked against the space **on the install
path only**: before a root is minted, no type and no collection in the
space — hidden or not, user or registered — may hold the bundle's
`xKey` (as its `xKey` or as its id). A hit is `409 type.xkey_conflict`
with `details.xKey` and `details.existingTypeId`, raised before
anything is created, so a refused setup leaves nothing behind (the
dependencies before it stand). The check runs only for a member who
may write (a reader's setup adopts or is refused by permission, never
by handle), and a root ever claimed for the same bundle id is never a
conflict — a leftover loser holding the handle is the registry's to
settle. The registered type ids and the meta ids `any`, `type` and
`collection` are refused as catalog handles at build time, so they
cannot collide at setup.

A definition that holds a catalog handle before the usecase is set up
— a user's own `contact` — blocks that usecase in that space: neither
types nor collections can be deleted over HTTP (`DELETE
…/types/:typeId` and `DELETE …/collections/:collectionId` are `501`).
To free the handle, rename it: `xkey` is an ordinary value in the
meta-type's namespace, so `POST …/properties/<typeId>/set/type` (or
`…/properties/<collectionId>/set/collection`) with `{"patch":
{"xkey": "contact_own"}}` renames it and the next setup proceeds. That
write is unguarded — the uniqueness check lives on `POST …/types` and
`POST …/collections` — so the client doing it owns the outcome,
including re-homing the objects that had the old definition.

An adopt heals a missing handle onto an installed root without this
check, so a space can end with two definitions holding one handle.
Catalog definitions resolve through the registry, never by scanning
handles, so this is visible only in `GET …/types?includeHidden=true`
and `GET …/collections?includeHidden=true`.

## What a client does with the reply

1. Read the catalog to offer usecases; read the space's bundles to see
   what is set up.
2. `POST /v1/catalog/<usecase>/setup` once per space per usecase (and
   again after a failure, or after a release that evolved the usecase),
   and keep the reply: per bundle, `typeId` or `collectionId` and the
   xKey → propId map.
3. Create objects with `type: "<typeId>"` and
   `collections: ["<collectionId>", …]`, write values under
   `<ownerId>.<propId>` (the owner being the object's type or one of
   its collections), query with `{"<ownerId>.<propId>": …}`,
   `{"any.type": "<typeId>"}` and `{"any.collections":
   "<collectionId>"}`. Never key by xKey on the wire.
4. Open a miniapp root by reading `miniapp.bundle` on it and running
   what that bundle id means to the client. A declaring root keeps the
   app's own state as records on itself — a namespaced records dataset
   is the storage collection `<rootId>_<key>`, a shared module dataset
   the module's canonical storage collection (`editor_blocks`,
   `chat_messages`); read either off `GET …/types/<rootId>/parts`.
5. **Resolve catalog types and collections through the bundle
   registry** — bundle id → `rootId` — never by scanning the type or
   collection list for the xKey: after a fork two roots hold the same
   xKey until the loser is resolved, and the registry names the
   winner. `relation.targetTypes` are type xKeys: for a catalog xKey
   go through the registry the same way; for a user type's xKey the
   type list is the only source.
6. Watch the space's bundles (`POST …/query[/subscribe]` on the
   `bundles` dataset of `spaceIndexObjectId`) for `losers` and run
   § Forks when one appears.

## Rendering

- An object has one type, so its `layout` is what renders — hidden or
  not. `hidden` keeps a definition out of pickers, nothing else.
- The object's collections contribute columns and nothing more: no
  layout, no parts.
- An app root that is itself the rendered object — the general chat —
  is a definition, so what renders is the layout it declares
  (`{"type": "chat"}`) over the parts it hosts.
- Then the type's parts in `pos` order, each as its `ui` says: a
  `records` part over the storage collection `<typeId>_<key>` on the
  object, an `editor` part a document (`editor_blocks` when shared —
  the body `page` has — `<typeId>_<key>` when namespaced), a `chat`
  part a chat.
- A type picker lists the catalog's listed types like any user type,
  a collection picker its listed collections, and both hide the roots
  the registry reports as losers.

## Forks

Every catalog root except the chat is a created root, so two devices
of one account that set a usecase up while apart each mint a root —
two `person` types with one xKey. After sync the registry names one
winner and lists the other in `losers`; objects that had the loser
keep their values under the loser's id. The client that observes a
loser re-homes those objects onto the winner **column by column
through the xKey map**: set the winner as the object's type (`POST
…/properties/:objectId/type/<winnerId>` replaces the loser) or, for a
collection, attach the winner and detach the loser (`POST` / `DELETE
…/properties/:objectId/collections/<id>`); copy each value from
`<loserId>.<propId>` to the winner's property of the same xKey; merge
a records host's records the same way (upsert into the winner's
storage collection); then `POST …/bundles/system%3A<name>%2Fv1/resolve`
with `{loserRootId}`. The server deletes the loser once its tree has
synced and it has been observed as a loser for five minutes
(`409 bundle.loser_not_ready` until then, retried in the background).
The server never merges: only the client knows what the values mean.
The bundle id carries a slash, so the path segment is percent-encoded.

## Evolution

Adopt heals what a root lacks and never patches, so a later release
may, without a new bundle id:

| change in the catalog | lands on existing installs |
|---|---|
| a property added to a type or a collection | at the next setup by a writer: written by handle under its deterministic id; a property definition the root already holds (live, or removed — the tombstone keeps the id) is left alone |
| parts declared on a type that never declared any | at the next setup by a writer: declared; a type with any part declaration (live or removed) is left alone |
| an `xKey` on a root that has none | at the next setup by a writer: filled in, never changed |
| a `miniapp` value added | at the next setup by a writer: written where absent, never overwritten; the root is filed under the built-in `miniapp` collection first when it is not already |
| an option key added to a `choice` property | at the next setup by a writer: the key is written with all of its catalog leaves (`name`, `color`, `pos`, and each `meta.<k>` separately); a key the definition already carries is left exactly as the space has it — renamed, recoloured, reordered or not |
| a bundle added to a usecase, a usecase added to `requires` | at the next setup: installed like any other step |

A failed `miniapp` value or option-key heal (a permission or sync
race) is not an error: the setup still answers 200 and the next setup
retries it.

Everything else on an existing install — an option's leaves once
present, other `xFormat` edits, display `name`, `layout` and
`hidden` — is the space's to change after install (client `PATCH`
calls on the space's copy); the catalog never patches or removes what
a root carries. The additive rule has one edge: an option key the
space deleted is absent, so the next setup writes it again — remove a
catalog option by shipping a catalog without it and leaving the space's
copy alone, not by deleting it in the space.

What needs a **new bundle id** (`/v2`, a new root; the old install
stays and the client migrates content): adding, changing or removing a
part or a dataset on a type that already declares parts (declared
once, all-or-nothing), changing a property's `kind` or `scope`
(pinned), renaming a type, collection or property xKey (the property
id derives from it), turning a `type` declaration into a `collection`
one or back (the root's marker is written once, at install), changing
`derived`.

Uninstall is `DELETE …/objects/<rootId>`: the id then reads as not
installed and a later setup mints a fresh root. There is no
usecase-level uninstall and no reference counting — a dependency stays
until its root is deleted, and deleting a definition other objects use
leaves their values orphaned (readable, no schema).

## Shipped usecases

| usecase | requires | bundle | declares |
|---|---|---|---|
| `wiki` | — | `system:wiki/v1` | collection `wiki` (hidden; `parentId`, `pos` — both kept out of the search index — and `folder`, a checkbox) + miniapp |
| `collections` | — | `system:collections/v1` | miniapp only, a `page` root — a feature switch: installing it turns on working with types in clients; it declares none of its own |
| `journal` | — | `system:journal/v1` | type `journal` (hidden; one `date`, a `date`-slug datetime) + shared editor `body` part + miniapp — one dated page per day |
| `meetings` | — | `system:meeting/v1` | type `meeting` (layout `page`; date, duration, participants, labels, words, source) with three surfaces — `notes` (the shared editor body), `summary` (a second, namespaced editor) and `transcript` (records, `idRule: user`, author-mutable and author-deletable, `skipHistory`, dynamic, search `text` under scope `meetings`) |
| | | `system:meetings/v1` | miniapp only, a `page` root — the sidebar entry that opens the meetings list |
| `tasks` | — | `system:task/v1` | type `task` (`notes`, `parent` → `project` / `area`, `planning` — a `choice` of inbox / anytime / someday, `planned` and `deadline` — `date` days, `completed`, `completedAt`, `position` — a rank kept out of the index) |
| | | `system:project/v1` | type `project` (`notes`, `parent` → `area`, `completed`, `completedAt`, `position`) |
| | | `system:area/v1` | marker type `area` — an area is a name and an icon; all three are listed types the planner creates and a table may show |
| | | `system:tasks/v1` | miniapp only, a `page` root — the sidebar entry that opens the planner |
| `general-chat` | — | `system:general-chat/v1` | derived, hidden; type `general_chat` with `layout {type: chat}` and one shared `chat` part — the reserved module's only declaration, the root its only host — + miniapp, so the chat is a sidebar entry |
| `people` | — | `system:person/v1` | type `person` (layout `profile`; email, phone, organization → `organization`, job_title, location, linkedin, birthday, tags) + shared editor `body` part |
| | | `system:organization/v1` | type `organization` (layout `profile`; kind, domain, categories, location, size, linkedin, main_contact → `person`) + shared editor `body` part |
| `contact` | `people` | `system:contact/v1` | collection `contact` (owner → `person`, status, source, referred_by → `person`, last_contact, next_follow_up) — the relationship facet a person or organisation is filed under |
| `investor` | `people` | `system:investor/v1` | collection `investor` (investor_type, investor_status, focus, stages, check_size, portfolio → `organization`) |
| `customer` | `people` | `system:customer/v1` | collection `customer` (account_status, plan, annual_value, customer_since, renewal_date) |
| `partner` | `people` | `system:partner/v1` | collection `partner` (partnership_type, partner_status, since, review_date) |
| `vendor` | `people` | `system:vendor/v1` | collection `vendor` (services, vendor_status, contract_value, renewal_date) |
| `cofounder` | `people` | `system:cofounder/v1` | collection `cofounder` (founded → `organization`, since, responsibilities, equity) |
| `candidate` | `people` | `system:candidate/v1` | collection `candidate` (role, candidate_stage, next_interview, resume) |
| `contacts` | `people`, `contact` | `system:contacts/v1` | miniapp, hidden; records part `layouts` (dataset `layouts`, `idRule: user` — the id is an identity type's xKey; field `blocks`, array) — a definition hosts itself, so the layouts live on the app root with no flag |
| `crm` | `contacts` | `system:deal/v1` | type `deal` (layout `profile`; stage, owner → `person`, organization → `organization`, amount, close_date) + shared editor `body` part |
| | | `system:crm/v1` | miniapp only, a `page` root |

Sixteen usecases, twenty-two bundles: nine types and eight collections
with an xKey. The identity is the type — `person`, `organization` —
and the relationship is the collection it is filed under, so one
person can be a customer and an investor at once, each facet carrying
its own columns. The two identities are one usecase because
`person.organization` and `organization.main_contact` reference each
other and the `requires` graph must stay acyclic; the roles are one
usecase each so a role lands only when picked (`crm` does not require
them); `deal` lives in `crm` because only the CRM needs it. `choice`
and `relation` properties are `kind: array` even when single-valued;
an amount is `currency` on `number`. The yaml is the authoritative
declaration.

**The sidebar is the `miniapp` collection.** Every catalog root that
declares `miniapp` is filed under it at birth (`rootCollections`), and
a client pins any other object with
`POST /v1/spaces/:spaceId/properties/:objectId/collections/miniapp`
and unpins it with the `DELETE` on the same path. The sidebar is
`{"any.collections": "miniapp"}`, minus bin members and
`miniapp.hidden`, sorted on `miniapp.pos`. Order (`miniapp.pos`, a
client-allocated lexid) and visibility (`miniapp.hidden`) are ordinary
property values —
`POST /v1/spaces/:spaceId/properties/:rootId/set/miniapp` with
`{"patch": {"pos": "a0"}}`. Setup writes neither, and never resets
them: it seeds only what the catalog declares, so a later setup
adopting an existing root leaves the reader's order and hides alone.
App roots other than the general chat are created roots, so the
ordinary permission, convergence and fork rules apply; a client
renders the registry's winner when two offline installs converge
(§ Forks).

**A wiki page is an ordinary object filed under the wiki
collection.** The wiki root is one object that is both the sidebar
entry (it is filed under `miniapp`) and the collection its pages are
filed under; the placement values live in that collection's namespace:

```
POST /v1/spaces/:spaceId/objects
{
  "type": "page",
  "collections": ["<wikiCollectionId>"],
  "initialProperties": {
    "<wikiCollectionId>": { "<parentIdPropId>": "", "<posPropId>": "a0", "<folderPropId>": false }
  }
}
```

`page` is the client's choice — any type works, and a folder is the
same create with `<folderPropId>` `true` and no `type`. Children of a
node, in order, are
`{"<wikiCollectionId>.<parentIdPropId>": "<parentObjectId>"}` sorted
on `<wikiCollectionId>.<posPropId>`; the whole tree is
`{"any.collections": "<wikiCollectionId>"}`, which the wiki root — the
definition — does not match itself. A move is one property write under
the same owner (`03-api.md` § The wiki tree).

**Journal** is one dated page per day: an entry's type is `journal`,
with a `date` value (a `date` slug on `kind: datetime` — midnight
UTC, `{"$date": …}`), and the type's `body` part shares the editor's
storage collection, so the entry has the same document a page has. The
type is hidden: the app creates entries, nothing picks the type from a
picker.

The catalog converges the TYPE, not the entries: an entry is an
ordinary object create, so two devices opening the same day while
apart write two pages for it. The rule clients share is **lowest id
wins per day** — read a day with `{"any.type": "<typeId>",
"<typeId>.<datePropId>": {"$date": "<day>T00:00:00.000Z"}}`, render
the lowest id, and leave the loser reachable rather than deleting
somebody's writing.

**A meeting is one object.** The `meeting` type says what it IS — when
it started, how long it ran, who took part, how it is classified — and
its three parts are the surfaces a client renders:

- **notes**, the editable document, on the editor's SHARED storage
  collection, so a meeting's body is the same body a page has
  (`…/editor/editor_blocks/**` on the meeting object);
- **summary**, a SECOND editor with a storage collection of its own
  (`<typeId>_summary`), because the condensed takeaways are a separate
  document from the notes somebody types during the call;
- **transcript**, one record per spoken turn (`startedAt`, `speaker`,
  `text`), read sorted on `startedAt`.

An ingest agent creates the object and writes all three; a reader
renders them and edits only the notes. `idRule: user` makes a
transcript record's id the provider's segment id, so re-ingesting a
turn is an upsert rather than a duplicate. Before writing an ingest:

- `dynamic` leaves a free keyspace beside the declared fields so the
  agent can carry a field the catalog does not declare — and an
  undeclared key has no author rule, so any writer of the space may
  set it.
- A deleted record's id is **burned** (`03-api.md` § Upsert records):
  deleting a turn and re-ingesting the same segment id answers `200`
  with an `upsert.record_deleted` rejection and writes nothing. An
  ingest reads `rejections`, never the status code alone.

Nothing is required — ingest is garbage-tolerant and readers render
placeholders — and every declared value field is `mutableBy: author`,
so the agent revises what it wrote while no other member may touch
those keys. Times are instants (`{"$date": …}`) like every other
timestamp, so date filters and the aggregation date operators work on
them. `participants` and `labels` are `choice`, so their values are
option KEYS: an ingest writes the key (minting the option on the
definition where it needs a new one), and a reader prints
`xFormat.options.<key>.name`, falling back to the key.

## Validation

The catalog is validated as a whole, every problem reported at once
with its yaml path, in three places that run the same code
(`server.ValidateCatalog`): at server boot (any problem refuses to
start, all of them listed), in the test suite (a broken embedded
catalog fails `make test`), and as a **build step** —
`make catalog-validate [FILES="candidate.yml …"]` checks the embedded
catalog and any candidate files, prints one
`<source>: <path>: <code>: <message>` line per problem (`<source>: ok`
for a clean source) and exits 1 on any problem, 2 when a candidate file
cannot be read. CI runs it on every pull request and before every
release build (`18-ci.md` § PR checks), so a broken catalog can neither
merge nor ship. Offline: no server, no space.

Problem codes of the structural layer (`internal/catalog`):

| code | meaning |
|---|---|
| `catalog.bad_yaml` | the source does not parse, or does not decode into the catalog shape |
| `catalog.unknown_field` | a key no catalog struct declares (strict decoding) |
| `catalog.bad_id` | a usecase id that is not a slug, a bundle id off `system:<name>/v<n>`, a type, collection or property xKey or a part key off its grammar |
| `catalog.duplicate` | a usecase id, bundle id, xKey (types and collections share one namespace; also when it equals a built-in id), property xKey, part key, dataset key or `requires` entry declared twice |
| `catalog.missing` | a required piece absent — an empty catalog, usecase name or bundles, bundle name, a declaration (`type` / `collection` / `miniapp` / `parts`), `rootType` on a bundle that declares nothing (every object has a type; `page` for a plain document), property xKey or kind, a dataset key, a relation's `targetTypes` |
| `catalog.bad_field` | a field that contradicts the rest — `type` next to `collection` ("a root defines a type or a collection, not both"), `parts` next to `collection` ("a collection declares no parts"), `hidden` without `type`, `collection` or `parts`, `rootType` next to a declaration or naming an id that is not a registered type, `meta` beyond `index`, `relation.filter`, a wrong module, `chat` not shared, `records` shared, fields on a module dataset, `deleteBy: author` or a `mutableBy: author` field without a creator stamp, a `search` mapping naming a field the dataset does not declare, a mapping key that is not a string, a node of the wrong shape, and the bounds (name ≤1024 B, ≤32 parts, ≤64 properties per declaration) |
| `catalog.unknown_usecase` | a `requires` entry naming no usecase |
| `catalog.cycle` | a self-require, or a cycle in `requires` — reported as its path (`a → b → a`) |
| `catalog.broken_link` | a `relation.targetTypes` xKey that is no type of the usecase, its transitive `requires` or a built-in; names the usecase that would have to be required when the type exists elsewhere in the catalog |
| `catalog.bad_miniapp` | `miniapp.bundle` differing from the bundle id, a key the built-in `miniapp` collection lacks, or a value of the wrong kind |

The server layer adds the descriptor gate and reports its own codes
verbatim at the offending property's or part's path:
`property.format_invalid` (a slug on the wrong kind, a bad option
shape), `request.schema` (a missing or unknown property kind, a bad
scope) and `request.invalid_field` / `request.missing_field` (a
malformed part or dataset draft, a layout that does not encode). The
SDK's own declaration validators do not run here — a draft only the
SDK would refuse surfaces at the first setup. Not checked: whether a
space already holds a type or a collection with a catalog handle (a
runtime `409 type.xkey_conflict` at setup), and the `derived` choice
(a policy, not a syntax).

## See also

- `03-api.md` § Catalog (endpoints), § Bundles (registry, derived
  roots, bundle-declared types and collections, resolve), § Collections
  (the built-in `miniapp` and `bin`), § The wiki tree
- `27-descriptors.md` (the `xFormat` vocabulary the catalog's
  properties use), `24-data-views.md` (views on a catalog definition: a
  `dataview` on the type or collection object)
- `16-chat.md` § Finding the chat object for a space, `25-favorites.md`
  (a client-registered bundle, for contrast)
- `18-ci.md` § PR checks (`catalog-validate`)
