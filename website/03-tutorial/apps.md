---
title: 4. Apps
description: The fourth level — parts let an object inherit a module's behaviour, a bundle makes every device converge on one definition, filing it under the miniapp collection puts it in the sidebar, and the catalog installs the well-known apps with one call.
order: 40
---
# 4. Apps

Everything so far was data. This part is about what a client does with it: how an object gains behaviour it never implements, how a type declared on one device becomes *the* type on every device, and how a thing in a space becomes an entry in the sidebar. The mailbox from [Part 3](datasets.html) ends up as an app.

`API`, `SPACE`, `MAILBOX` and `INBOX` carry over from Part 3. The bundle below creates a separate root; the sample messages stay in your existing inbox.

## Parts are inheritance, one level deep

A type is properties plus **parts**. A part is a display unit the client renders — a table, a body, a transcript — and each part owns storage collections that a **module** serves:

| Module | Serves | You declared one in |
|--------|--------|---------------------|
| `records` | records whose schema you declare, in `<typeId>_<key>` | Part 3 — the `messages` part |
| `editor` | block documents with a lossless markdown bridge, in the shared `editor_blocks` | below |
| `chat` | messages with reactions, mentions and read tracking, in `chat_messages` | reserved to the server — the space's one chat |

Giving the mailbox a notes body is one more part on the type:

```bash
curl -s -X POST $API/spaces/$SPACE/types/$MAILBOX/parts -H 'content-type: application/json' \
  -d '{"key": "notes", "name": "Notes", "pos": "a1", "ui": {"type": "document"},
       "datasets": [{"module": "editor", "shared": true}]}'

curl -s -X POST $API/spaces/$SPACE/objects/$INBOX/editor/editor_blocks/markdown/append \
  -H 'content-type: application/json' \
  -d '{"content": "# Inbox notes\n\n- [ ] archive 2025 invoices"}'
```

The mailbox object did not learn how to store blocks, merge concurrent edits or render markdown. It **inherited** that from the editor module by carrying a type whose part names it — the way a class in C++ or Java inherits a method and its private state from a base class without reimplementing either. The part is the encapsulated unit: it knows how its content is stored (`editor_blocks`) and how it is written (the block and markdown endpoints), and the client renders it as a document ([Editor](../types/editor.html)).

Two rules keep this flat rather than a hierarchy:

- **Types never inherit from types.** An object has exactly one type, and that type's parts are the object's parts. There is no chain to resolve and no primary-type contest.
- **A shared storage collection is the same body everywhere.** `"shared": true` puts the body in the module's canonical storage collection, `editor_blocks` — the one the built-in `page` uses too. Retype an object from `mailbox` to `page` and its notes are still there, because both parts name the same place ([Modules](../types/index.html)).

> **Why it matters.** The modules are the shapes that are hardest to get right as CRDTs — a block document, a messenger — shipped once and inherited everywhere. Your type gets a real collaborative body by naming a module; the merge rules run on every peer and never reach a server.

## Telling the client what to render

The server never renders anything, but the type carries the hints a client keys on:

```bash
curl -s -X PATCH $API/spaces/$SPACE/types/$MAILBOX -H 'content-type: application/json' \
  -d '{"layout": {"type": "table"}}'

any type update $SPACE $MAILBOX --layout '{"type":"table"}'
```

| Hint | On | Meaning |
|------|----|---------|
| `layout` | the type | The descriptor the client renders the object with — `{"type": …, "config": …}`, your vocabulary, opaque to the server. Collections carry none. |
| `ui` | each part | The widget for that part (`table`, `document`, `board`, `chat`, …), also client vocabulary. |
| `pos` | each part | Part order, a lexid string. |

The client rule is one line:

```
object view     = layout of any.type + parts of any.type, in pos order
property groups = the type's properties + every collection's properties
```

A mailbox filed under the wiki collection renders as a mailbox and still has a place in the tree; the wiki's `parentId` / `pos` / `folder` show up as a property group, not as a part. Rendering only the type's property group — and dropping the collections' columns — is the common mistake.

## Bundles: one definition on every device

The `mailbox` type from Part 3 has a problem you cannot see on one machine. Set it up on your laptop and your phone while they are apart and each mints its own `mailbox` — two types, one handle, and the CRDT faithfully keeps both. In a local-first system there is no moment where "the mailbox type" gets created once.

A **bundle** fixes that. It is one root object registered in the space under a permanent id, with the whole type declared on it, and the registry converges every device on one root — whoever installs second adopts the first one's type. Declare the mailbox as a bundle (the space already holds the hand-made type under the handle `mailbox`, so the bundle takes its own):

```bash
curl -s -X POST $API/spaces/$SPACE/bundles -H 'content-type: application/json' -d '{
  "id": "mail/v1", "name": "Mail", "xKey": "mail", "layout": {"type": "table"},
  "properties": [ {"xKey": "address", "name": "Address", "kind": "string", "xFormat": {"type": "email"}} ],
  "parts": [
    {"key": "messages", "ui": {"type": "table"},
     "datasets": [{"key": "messages", "idRule": "user",
                   "search": {"title": "subject", "text": ["from", "body"]},
                   "fields": [{"key": "subject", "kind": "string", "required": true},
                              {"key": "from", "kind": "string", "required": true},
                              {"key": "body", "kind": "string"},
                              {"key": "receivedAt", "kind": "datetime", "required": true},
                              {"key": "read", "kind": "boolean", "mutableBy": "any"}]}]},
    {"key": "notes", "ui": {"type": "document"},
     "datasets": [{"module": "editor", "shared": true}]} ],
  "rootCollections": ["miniapp"],
  "rootProperties": {"miniapp": {"bundle": "mail/v1", "pos": "a0"}} }'
```

```json
{ "bundle": { "id": "mail/v1", "rootId": "bafyreim…", "roots": ["bafyreim…"], "losers": [], "derived": false },
  "installed": true }
```

```bash
any bundle ensure $SPACE --body @mail-bundle.json
```

The reply's `rootId` is three things at once:

1. **the type** — `typeId == rootId`; `GET …/types/<rootId>/properties` gives the `xKey → propId` map you cache;
2. **the host of its own records** — a definition hosts itself, with no flag and no self-membership: `"objectId": "<rootId>", "dataset": "<rootId>_messages"` on `/upsert` and `/query` is the inbox. The root's own type slot holds the marker `__type__`, not its id, so the root never turns up among the objects of its type;
3. **the app** — `rootCollections` filed the root under the built-in `miniapp` collection and `rootProperties` set its `bundle`, so it is a sidebar entry (next section).

Run the same call on the second device and it answers `installed: false` with the same `rootId`: an adopt, a pure read, safe for a member who could not create anything. Two devices that install while genuinely apart each register a root; after sync the registry names one winner and lists the other under `losers`, and the client merges and resolves ([Bundles](../collaboration/bundles.html)).

Property ids on a bundle are derived from `(rootId, xKey)`, which is why two blind installs mint one `address` column per handle rather than two.

## The sidebar: `miniapp`

A space is, to its user, a list of apps. Being in the sidebar is a categorisation of an object, not a change to what it is — so the marker is a **collection**: the built-in hidden `miniapp`, with three columns and nothing else. An app root keeps its own type slot for its definition marker, and a pinned object keeps the type it already has:

| Property | Meaning |
|----------|---------|
| `bundle` | The installed bundle's id — what the client runs when the entry is opened. Absent on an object the user merely pinned. |
| `pos` | Sidebar position, a lexid the client allocates. |
| `hidden` | Out of the sidebar without uninstalling anything. |

The sidebar is one subscription (it blocks the terminal — Ctrl-C before the pin examples below):

```bash
curl -s -N -X POST $API/spaces/$SPACE/objects/query/subscribe -H 'content-type: application/json' -d '{
  "filter": {"$and": [{"any.collections": "miniapp"},
                      {"any.collections": {"$nin": ["bin"]}},
                      {"miniapp.hidden": {"$ne": true}}]},
  "sort": ["miniapp.pos"]}'
```

A row with `miniapp.bundle` is an installed app and the bundle id says what to run — `mail/v1` opens your mailbox UI, `system:wiki/v1` the wiki. A row without one is an object the user pinned, rendered as the object it is. Pinning the Part 3 inbox:

```bash
curl -s -X POST $API/spaces/$SPACE/properties/$INBOX/collections/miniapp     # pin
curl -s -X POST $API/spaces/$SPACE/properties/$INBOX/set/miniapp \
  -H 'content-type: application/json' -d '{"patch": {"pos": "a2"}}'
curl -s -X DELETE $API/spaces/$SPACE/properties/$INBOX/collections/miniapp   # unpin
```

The marker obliges nothing. A plain-text notebook you wrote yourself, declared as a miniapp, is a valid entry point: "this is my notebook, this is where I start". All the machinery above it is optional.

## The catalog: the well-known apps

The server ships a catalog of well-known bundles grouped into **usecases**, and installs one into a space with a single call, dependencies included:

```bash
curl -s $API/catalog                                    # the usecases
curl -s -X POST $API/catalog/wiki/setup -H 'content-type: application/json' \
  -d '{"spaceId": "'$SPACE'"}'

any catalog list
any catalog setup wiki $SPACE
```

```json
{ "usecase": "wiki",
  "bundles": [ { "usecase": "wiki", "id": "system:wiki/v1",
                 "bundle": {"id": "system:wiki/v1", "rootId": "<rootId>", …},
                 "installed": true, "collectionId": "<rootId>",
                 "properties": {"parentId": "…", "pos": "…", "folder": "…"},
                 "miniapp": {"bundle": "system:wiki/v1"} } ] }
```

Every catalog entry is the same construction you just built by hand, and each shows a different mix of the three roles:

| Usecase | The root is |
|---------|-------------|
| `wiki` | an app **and** a collection: the sidebar entry, and the hidden collection whose `parentId` / `pos` / `folder` place every page filed under it in the tree (a page keeps its own type) |
| `collections` | an app only — a `page` root filed under `miniapp`, whose presence switches the types feature on in the client |
| `journal` | an app **and** a collection: the sidebar entry, and the hidden collection whose one `date` property makes a page filed under it that day's entry |
| `meetings` | two roots: the `meeting` type, outside the sidebar — a meeting is one object whose three parts are its notes (the shared editor), a second editor for the summary, and a transcript dataset an agent fills — and a separate `page` root that is the sidebar entry |
| `general-chat` | the space's one chat: a **derived** root both sides of a partition compute, so it can never fork, carrying the reserved `chat` module |
| `people`, `contact`, `contacts`, `crm` | a set: `crm` requires `contacts`, which requires `people` and `contact`; setup resolves the closure in order and the reply lists every bundle it touched, `typeId` for a type root and `collectionId` for a collection root |

Every app that ships with a client belongs here, types included: the catalog is the one place a well-known type is declared, so two clients — or a client and an agent — resolve the same ids instead of each minting a type by name and hoping they match.

Setup is idempotent — run it on every device that needs the feature and each adopts the same roots with byte-identical property ids. A space where the wiki was never set up has no wiki collection in it at all: nothing from a usecase you do not use lands in your space.

What a usecase installs is ordinary definitions. `profile` is the one format a person and an organisation share; `person` and `organization` are collections of profiles that reference each other through relation properties; `contact` — like `investor`, `customer`, `partner`, `vendor`, `cofounder` and `candidate` — is a **collection** too, so a person you actively manage stays a `profile` filed under `person`, and gains the contact columns by being filed under `contact`. Keeping the format and the base collections in their own usecase is how several usecases share them: `people` is required by every role, and installing a role installs it ([Well-known bundles](../collaboration/bundles.html)).

> **Note.** Nothing here stops a user from opening the `person` collection and editing it. Setup is additive: the next run writes what a later catalog added and the root lacks — a property under its deterministic id, an option key, a `miniapp` value — and leaves what the definition carries as the space has it, renamed or recoloured. A property the user removed stays removed; an option key the user deleted is absent, so setup writes it again ([Bundles](../collaboration/bundles.html)).

## Where this ends

Read from the database up, an app is the complicated thing: a type, its parts, a bundle root, a sidebar marker. Read from the interface down, it is the simplest thing there is — one entry you tap — and objects, types and datasets are the details inside it that you can go and look at when you want to extend it. Both readings are true, and the levels of this tutorial are the path between them: stop at Part 1 with a notebook, at Part 2 with a password manager, at Part 3 with a mailbox, or take a ready bundle from the catalog and never think about what is inside until you need to.

Reference: [Modules](../types/index.html), [Bundles](../collaboration/bundles.html), [Types and properties](../database/types-and-properties.html), [Collections](../database/collections.html).
