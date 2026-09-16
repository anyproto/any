---
title: 2. Properties
description: The second level — a type with typed properties gives objects columns you can validate, filter and sort on, and a collection adds a second group on top. Built here as a small password manager.
order: 20
---
# 2. Properties

Define a `credential` type, add five properties, and create an example GitHub credential. Then file that object under a subscription collection to add a price and renewal date. Use the dummy values shown here.

**Prerequisites:** the running server and `API` / `SPACE` variables from [tutorial setup](index.html#before-you-start). This part creates its own type and object; it does not reuse Part 1's deleted page.

A type says what an object **is** and supplies its columns and layout. A collection adds another group of columns while the object keeps its type.

## Create the type

```bash
CRED=$(curl -s -X POST $API/spaces/$SPACE/types -H 'content-type: application/json' \
  -d '{"name": "Credential", "xKey": "credential"}' | jq -r .typeId)
```

`xKey` is required: it is the programmatic handle the type resolves by, and it must survive renames — the display `name` can change freely. A handle already used by another type or collection in the space is `409 type.xkey_conflict` — the two share one namespace.

## Add properties

Properties are added one at a time. Each has a `kind` — the storage contract, pinned for life — and an `xFormat` descriptor that says what the value means:

```bash
add() { curl -s -X POST $API/spaces/$SPACE/types/$CRED/properties \
          -H 'content-type: application/json' -d "$1" | jq -r .propId; }

SITE=$(add '{"name": "Site",     "xKey": "site",     "kind": "string", "xFormat": {"type": "url"}}')
LOGIN=$(add '{"name": "Login",   "xKey": "login",    "kind": "string", "xFormat": {"type": "text"}}')
SECRET=$(add '{"name": "Secret", "xKey": "secret",   "kind": "string", "xFormat": {"type": "text"}}')
ROTATED=$(add '{"name": "Rotated", "xKey": "rotated", "kind": "datetime", "xFormat": {"type": "date"}}')
CATEGORY=$(add '{"name": "Category", "xKey": "category", "kind": "array",
  "xFormat": {"type": "choice", "options": {
    "work":    {"name": "Work",    "color": "blue",  "pos": "a0"},
    "personal":{"name": "Personal","color": "green", "pos": "a1"}}}}')
```

The equivalent CLI call is shown below. Do not run it after the HTTP call has already added `site`.

```bash
any type property add $SPACE $CRED --name Site --xkey site --kind string --x-format '{"type":"url"}'
```

| Kind | Descriptor `type` | Value on the wire |
|------|-------------------|-------------------|
| `string` | `text`, `url`, `email`, `phone` | a string |
| `number` | `number`, `currency`, `percent` | a number |
| `boolean` | `checkbox` | `true` / `false` |
| `datetime` | `date`, `datetime` | `{"$date": "2026-09-08T00:00:00.000Z"}` — a `date` lands on midnight UTC |
| `array` | `choice` | an array of option **keys**: `["work"]` even for a single choice |
| `array` | `relation` | `["any://<objectId>"]` — links to other objects |

The kind is what the server enforces on every write; the descriptor is a hint for clients and can be changed later. The full vocabulary is in [Data types](../database/data-types.html).

## The three ids

You now hold three different identifiers, and they are never interchangeable:

| id | what it is | where it comes from |
|----|------------|---------------------|
| `typeId` (`$CRED`) | the content-addressed id of the type | `POST …/types`, `GET …/types` |
| `xKey` (`site`) | the stable handle you declared | your own code |
| `propId` (`$SITE`) | the content-addressed id of a property — **the write key** | `GET …/types/:typeId/properties` |

Values are stored and written by `propId`, never by xKey: a write keyed by an xKey fails with `property.not_found`. A client resolves `xKey → propId` once from the property list and caches the map:

```bash
curl -s $API/spaces/$SPACE/types/$CRED/properties
# → {"properties": [{"id": "<propId>", "xKey": "site", "name": "Site", "kind": "string", "xFormat": {…}}, …]}
```

## Store a credential

Set the type at create and pass its values under its id. The `any` group still holds the name:

```bash
GH=$(curl -s -X POST $API/spaces/$SPACE/objects -H 'content-type: application/json' -d '{
  "type": "'$CRED'",
  "initialProperties": {
    "any":     {"name": "GitHub"},
    "'$CRED'": {"'$SITE'": "https://github.com", "'$LOGIN'": "ada", "'$SECRET'": "correct horse battery staple",
                "'$CATEGORY'": ["work"], "'$ROTATED'": {"$date": "2026-09-01T00:00:00Z"}}}}' | jq -r .objectId)
```

Notice where the values went: under the type's id, not at the top of the object. A type is a **namespace** for properties, and there are no global properties. Every property belongs to exactly one owner, is defined there, and its value lives on the object under that owner's id — even `name` and `description` sit in the universal `any` namespace rather than on the object itself. The property list of an owner is the whole vocabulary you can write under its id, and nothing outside that list is a property at all.

Writes validate both the storage kind and the descriptor. A number in a string property is `400 property.kind_mismatch`; a value that violates the descriptor can be `400 property.format_violation`. Datetimes need the `$date` wrapper and choice values need an array. The property list is the contract to validate against.

Later writes go through the owner-scoped set:

```bash
curl -s -X POST $API/spaces/$SPACE/properties/$GH/set/$CRED -H 'content-type: application/json' \
  -d '{"patch": {"'$SECRET'": "new secret", "'$ROTATED'": {"$date": "2026-09-08T00:00:00Z"}}}'
```

> **Why it matters.** The secret is a CRDT value in a space that is end-to-end encrypted on your device; relay nodes carry ciphertext and hold no key ([Encryption](../understanding/encryption.html)). A password manager at this level is one type and five properties — there is no vault format to design and no server to trust with plaintext.

## Ask questions of the columns

Property paths on the wire are `<ownerId>.<propId>`. Filter and sort on them like any other field:

```bash
# every work credential, oldest rotation first
curl -s -X POST $API/spaces/$SPACE/objects/query -H 'content-type: application/json' -d '{
  "filter": {"$and": [{"any.type": "'$CRED'"},
                      {"any.collections": {"$nin": ["bin"]}},
                      {"'$CRED.$CATEGORY'": "work"}]},
  "sort": ["'$CRED.$ROTATED'"], "limit": 50}'
```

```bash
# rotated more than 90 days ago
curl -s -X POST $API/spaces/$SPACE/objects/query -H 'content-type: application/json' -d '{
  "filter": {"'$CRED.$ROTATED'": {"$lt": {"$date": "2026-06-10T00:00:00Z"}}}}'
```

Two things to notice. `{"any.type": "<typeId>"}` is "objects of this type" — plain equality on a scalar, because an object has exactly one type. And the one exclusion. A type definition is itself an object row, but it carries the marker `__type__` in `any.type` rather than its own id, so it never turns up among its own objects and needs no exclusion clause. A binned object keeps its type, so an ordinary list leaves out `bin` members with `{"any.collections": {"$nin": ["bin"]}}` ([Reading data](../database/reading-data.html)).

The same body against `…/objects/query/subscribe` is a live list — the vault view of your password manager updates as entries change on any device.

## One object, one type and any number of collections

The type says what the object **is**, and there is exactly one of it. Everything else an object belongs to is a **collection**: a group of columns you file objects under, with no layout and no parts of its own. An object can be filed under any number of them, each contributing its own group of columns keyed by its id, and none of them knows about the rest.

Say the GitHub account is also something you pay for. "Paid subscription" is not what the object *is* — it is still a credential — so it is a collection, `subscription`, with a price and a renewal date:

```bash
SUB=$(curl -s -X POST $API/spaces/$SPACE/collections -H 'content-type: application/json' \
  -d '{"name": "Subscription", "xKey": "subscription"}' | jq -r .collectionId)
PRICE=$(curl -s -X POST $API/spaces/$SPACE/collections/$SUB/properties -H 'content-type: application/json' \
  -d '{"name": "Price", "xKey": "price", "kind": "number", "xFormat": {"type": "currency"}}' | jq -r .propId)
RENEWS=$(curl -s -X POST $API/spaces/$SPACE/collections/$SUB/properties -H 'content-type: application/json' \
  -d '{"name": "Renews", "xKey": "renews", "kind": "datetime", "xFormat": {"type": "date"}}' | jq -r .propId)

curl -s -X POST $API/spaces/$SPACE/properties/$GH/collections/$SUB      # file it under the collection
curl -s -X POST $API/spaces/$SPACE/properties/$GH/set/$SUB -H 'content-type: application/json' \
  -d '{"patch": {"'$PRICE'": 4, "'$RENEWS'": {"$date": "2026-10-01T00:00:00Z"}}}'
```

For a new collection, the CLI equivalents are `any collection create $SPACE --name Subscription --xkey subscription` and `any object collection attach $SPACE $GH $SUB`. Use the returned collection ID as `SUB`; skip the create if you already ran the HTTP example.

Creating a collection is the type create minus `layout`, and its four property routes are the type ones with a different owner segment — same bodies, same patch grammar, same error codes.

Read the row back and it holds both groups, with `any.type` naming the one type and `any.collections` listing what it is filed under:

```json
{ "id": "bafyreig…",
  "any": { "name": "GitHub", "type": "<credentialTypeId>", "collections": ["<subscriptionCollectionId>"] },
  "<credentialTypeId>":           { "<site>": "https://github.com", "<login>": "ada", "…": "…" },
  "<subscriptionCollectionId>":   { "<price>": 4, "<renews>": {"$date": "2026-10-01T00:00:00.000Z"} } }
```

The object now answers to both questions: `{"any.type": "'$CRED'"}` lists it among the credentials, `{"any.collections": "'$SUB'"}` among the subscriptions, and a sort on `<subscriptionCollectionId>.<renews>` puts it in the renewal calendar. Passing `"collections": ["'$SUB'"]` at create does the same in one call; `DELETE …/properties/$GH/collections/$SUB` unfiles it again.

Three rules make this simple rather than clever:

- **One type, any number of collections.** The type is what the object is — replacing it with `POST …/properties/:objectId/type/:typeId` swaps the whole answer, and there is no unset. Filing and unfiling a collection is additive and idempotent, and neither is a delete: the old group's values stay on the row as read-tolerant orphan data, and setting the owner again brings them back.
- **Column names never collide.** Each owner is its own namespace, so paths are `<ownerId>.<propId>` and a type and a collection can both have a `name` or a `date` property with the values kept apart. Filing an object is opening a second namespace on it, not merging columns into one flat row.
- **Layout and parts come from the type alone; property groups come from every owner.** Collections have no layout and no parts, but each adds its columns. A client renders the credential layout and shows the subscription columns in the property panel next to the credential ones.

Pick a type when the thing needs a layout, a body or any other part — Credential, Person, Task. Pick a collection when it is a facet on objects that keep their own type — Subscription, Reading list, Q3 launch ([Collections](../database/collections.html)).

## Change the definition, not the data

A property definition is a synced record like the values. Rename it, reorder it, add an option — none of that touches stored values:

```bash
any type property patch $SPACE $CRED $SECRET --set '{"name": "Password"}'
any type property option set $SPACE $CRED $CATEGORY finance --name Finance --color amber --pos a2
```

A choice value stores the option **key** (`work`), never its label, so renaming an option is one definition write and zero object writes. What you cannot change is `kind`: it is the one thing every peer has already relied on when validating writes (`400 property.immutable`). Details in [Property lifecycle](../database/property-lifecycle.html).

## Where this level ends

You have objects with typed, validated, filterable columns — a schema that syncs with its data — and a way to stack a second group of columns on top without changing what the object is. It scales to hundreds of credentials, contacts or books: one object per thing, one row each in the `objects` storage collection.

It does not scale to a mailbox. Ten thousand emails as ten thousand objects means ten thousand rows in the space-wide storage collection, each with its own change history, for things that belong together, arrive in bulk and are mostly read as one list. The next part keeps them as **records on a single object** — a dataset with its own enforced schema, ids you supply, and an import that is safe to re-run.

Next: [3. Datasets](datasets.html). Reference: [Types and properties](../database/types-and-properties.html), [Collections](../database/collections.html).
