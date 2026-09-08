---
title: 4. Apps
description: The fourth level — parts let an object inherit a module's behaviour, a bundle makes every device converge on one definition, the miniapp marker puts it in the sidebar, and the catalog installs the well-known apps with one call.
order: 40
---
# 4. Apps

Everything so far was data. This part is about what a client does with it: how an object gains behaviour it never implements, how a type declared on one device becomes *the* type on every device, and how a thing in a space becomes an entry in the sidebar. The mailbox from [Part 3](datasets.html) ends up as an app.

## Parts are inheritance, one level deep

A type is properties plus **parts**. A part is a display unit the client renders — a table, a body, a transcript — and each part owns datasets that a **module** serves:

| Module | Serves | You declared one in |
|--------|--------|---------------------|
| `records` | a dataset whose schema you declare, in `<typeId>_<key>` | Part 3 — the `messages` part |
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

- **Types never inherit from types.** An object carries several types side by side; each contributes its columns and its parts. There is no chain to resolve.
- **A shared collection appears once.** `"shared": true` puts the body in the module's canonical collection. An object carrying the mailbox *and* the built-in `page` has one body, not two — which is exactly the diamond problem, answered by construction instead of by a resolution order ([Modules](../types/index.html)).

> **Why it matters.** The modules are the shapes that are hardest to get right as CRDTs — a block document, a messenger — shipped once and inherited everywhere. Your type gets a real collaborative body by naming a module; the merge rules run on every peer and never reach a server.

## Telling the client what to render

The server never renders anything, but the type carries the hints a client keys on:

```bash
curl -s -X PATCH $API/spaces/$SPACE/types/$MAILBOX -H 'content-type: application/json' \
  -d '{"weight": 10, "layout": {"type": "table"}}'

any type update $SPACE $MAILBOX --weight 10 --layout '{"type":"table"}'
```

| Hint | On | Meaning |
|------|----|---------|
| `weight` | the type | An object's **primary** type is the carried type with the highest weight. Built-ins carry none and never win. |
| `layout` | the type | The descriptor the client renders for the primary type — `{"type": …, "config": …}`, your vocabulary, opaque to the server. |
| `ui` | each part | The widget for that part (`table`, `document`, `board`, `chat`, …), also client vocabulary. |
| `pos` | each part | Part order, a lexid string. |

The client rule is: take the object's primary type, render its `layout` with the parts of **every** carried type in `pos` order. A mailbox that also carries the wiki type renders as a mailbox and still has a place in the tree; a person that also carries `page` still shows its body. Rendering only the primary type's parts is the common mistake.

## Bundles: one definition on every device

The `mailbox` type from Part 3 has a problem you cannot see on one machine. Set it up on your laptop and your phone while they are apart and each mints its own `mailbox` — two types, one handle, and the CRDT faithfully keeps both. In a local-first system there is no moment where "the mailbox type" gets created once.

A **bundle** fixes that. It is one root object registered in the space under a permanent id, with the whole type declared on it, and the registry converges every device on one root — whoever installs second adopts the first one's type. Declare the mailbox as a bundle (the space already holds the hand-made type under the handle `mailbox`, so the bundle takes its own):

```bash
curl -s -X POST $API/spaces/$SPACE/bundles -H 'content-type: application/json' -d '{
  "id": "mail/v1", "name": "Mail", "xKey": "mail", "weight": 10, "layout": {"type": "table"},
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
  "rootTypes": ["miniapp"],
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
2. **an object of that type** — the root carries itself, so it can hold the mailbox's own records: `"objectId": "<rootId>", "dataset": "<rootId>_messages"` on `/upsert` and `/query` is the inbox;
3. **the app** — `rootTypes` attached the built-in `miniapp` type and `rootProperties` set its `bundle`, so the root is a sidebar entry (next section).

Run the same call on the second device and it answers `installed: false` with the same `rootId`: an adopt, a pure read, safe for a member who could not create anything. Two devices that install while genuinely apart each register a root; after sync the registry names one winner and lists the other under `losers`, and the client merges and resolves ([Bundles](../collaboration/bundles.html)).

Property ids on a bundle are derived from `(rootId, xKey)`, which is why two blind installs mint one `address` column per handle rather than two.

## The sidebar: `miniapp`

A space is, to its user, a list of apps. The marker is the built-in hidden type `miniapp`, with three properties and no parts:

| Property | Meaning |
|----------|---------|
| `bundle` | The installed bundle's id — what the client runs when the entry is opened. Absent on an object the user merely pinned. |
| `pos` | Sidebar position, a lexid the client allocates. |
| `hidden` | Out of the sidebar without uninstalling anything. |

The sidebar is one subscription:

```bash
curl -s -N -X POST $API/spaces/$SPACE/objects/query/subscribe -H 'content-type: application/json' -d '{
  "filter": {"$and": [{"any.types": "miniapp"},
                      {"any.types": {"$nin": ["bin"]}},
                      {"miniapp.hidden": {"$ne": true}}]},
  "sort": ["miniapp.pos"]}'
```

A row with `miniapp.bundle` is an installed app and the bundle id says what to run — `mail/v1` opens your mailbox UI, `system:wiki/v1` the wiki. A row without one is an object the user pinned, rendered as the object it is:

```bash
curl -s -X POST $API/spaces/$SPACE/properties/$OBJ/attach/miniapp          # pin
curl -s -X POST $API/spaces/$SPACE/properties/$OBJ/set/miniapp \
  -H 'content-type: application/json' -d '{"patch": {"pos": "a2"}}'
curl -s -X POST $API/spaces/$SPACE/properties/$OBJ/detach/miniapp          # unpin
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
  "bundles": [ { "id": "system:wiki/v1", "installed": true, "typeId": "<rootId>",
                 "properties": {"parentId": "…", "pos": "…", "folder": "…"},
                 "miniapp": {"bundle": "system:wiki/v1"} } ] }
```

Every catalog entry is the same construction you just built by hand, and each shows a different mix of the three roles:

| Usecase | The root is |
|---------|-------------|
| `wiki` | an app **and** a type: the sidebar entry, and the hidden type whose `parentId` / `pos` / `folder` place every page in the tree |
| `collections` | an app only — an empty `miniapp` root whose presence switches the types feature on in the client |
| `general-chat` | the space's one chat: a **derived** root both sides of a partition compute, so it can never fork, carrying the reserved `chat` module |
| `people`, `contact`, `contacts`, `crm` | a set: `crm` requires `contacts`, which requires `people` and `contact`; setup resolves the closure in order and the reply lists every bundle it touched |

Setup is idempotent — run it on every device that needs the feature and each adopts the same roots with byte-identical property ids. A space where the wiki was never set up has no wiki type in it at all: nothing from a usecase you do not use lands in your space.

The types a usecase installs are ordinary user types. `person` and `organization` reference each other through relation properties; `contact` is a second, lighter type attached to a person you actively manage, with a lower weight so the person's profile keeps rendering. Splitting a base type into its own bundle is how several usecases share it: `people` is required by every role, and installing a role installs it ([Well-known bundles](../collaboration/bundles.html)).

> **Note.** Nothing here stops a user from opening the `person` type and deleting its properties. The catalog heals what a root lacks on the next setup — a missing property or option is written back under its deterministic id — but it never overwrites what the space changed on purpose.

## Where this ends

Read from the database up, an app is the complicated thing: a type, its parts, a bundle root, a sidebar marker. Read from the interface down, it is the simplest thing there is — one entry you tap — and objects, types and datasets are the details inside it that you can go and look at when you want to extend it. Both readings are true, and the levels of this tutorial are the path between them: stop at Part 1 with a notebook, at Part 2 with a password manager, at Part 3 with a mailbox, or take a ready bundle from the catalog and never think about what is inside until you need to.

Reference: [Modules](../types/index.html), [Bundles](../collaboration/bundles.html), [Types and properties](../database/types-and-properties.html).
