---
title: 2. Properties
description: The second level — a type with typed properties gives objects columns you can validate, filter and sort on, and one object can carry several types. Built here as a small password manager.
order: 20
---
# 2. Properties

A type is a named set of property definitions an object can carry. Attach it to an object and the object gains those columns: each with a kind the server checks, a descriptor that says how to render it, and a stable handle you resolve it by. This part builds a password manager — a `credential` type — and it never needs anything beyond this level.

## Create the type

```bash
CRED=$(curl -s -X POST $API/spaces/$SPACE/types -H 'content-type: application/json' \
  -d '{"name": "Credential", "xKey": "credential"}' | jq -r .typeId)

any type create $SPACE --name Credential --xkey credential
```

`xKey` is required: it is the programmatic handle the type resolves by, and it must survive renames — the display `name` can change freely. A handle already used by another type in the space is `409 type.xkey_conflict`.

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

Values are stored and written by `propId`. The server never sees an xKey: a write keyed by one fails with `property.not_found`. A client resolves `xKey → propId` once from the property list and caches the map:

```bash
curl -s $API/spaces/$SPACE/types/$CRED/properties
# → {"properties": [{"id": "<propId>", "xKey": "site", "name": "Site", "kind": "string", "xFormat": {…}}, …]}
```

## Store a credential

Attach the type at create and pass its values under its id. The `any` group still holds the name:

```bash
GH=$(curl -s -X POST $API/spaces/$SPACE/objects -H 'content-type: application/json' -d '{
  "types": ["'$CRED'"],
  "initialProperties": {
    "any":     {"name": "GitHub"},
    "'$CRED'": {"'$SITE'": "https://github.com", "'$LOGIN'": "ada", "'$SECRET'": "correct horse battery staple",
                "'$CATEGORY'": ["work"], "'$ROTATED'": {"$date": "2026-09-01T00:00:00Z"}}}}' | jq -r .objectId)
```

Notice where the values went: under the type's id, not at the top of the object. A type is a **namespace** for properties, and there are no global properties. Every property belongs to exactly one type, is defined there, and its value lives on the object under that type's id — even `name` and `description` sit in the universal `any` namespace rather than on the object itself. The property list of a type is the whole vocabulary you can write under its id, and nothing outside that list is a property at all.

A value that does not fit its declared format — a number under `site`, a bare string under `rotated`, an option not wrapped in an array — is refused with `400 property.format_violation`, so the type is a contract, not a convention.

Later writes go through the type-scoped set:

```bash
curl -s -X POST $API/spaces/$SPACE/properties/$GH/set/$CRED -H 'content-type: application/json' \
  -d '{"patch": {"'$SECRET'": "new secret", "'$ROTATED'": {"$date": "2026-09-08T00:00:00Z"}}}'
```

> **Why it matters.** The secret is a CRDT value in a space that is end-to-end encrypted on your device; relay nodes carry ciphertext and hold no key ([Encryption](../understanding/encryption.html)). A password manager at this level is one type and five properties — there is no vault format to design and no server to trust with plaintext.

## Ask questions of the columns

Property paths on the wire are `<typeId>.<propId>`. Filter and sort on them like any other field:

```bash
# every work credential, oldest rotation first
curl -s -X POST $API/spaces/$SPACE/objects/query -H 'content-type: application/json' -d '{
  "filter": {"$and": [{"any.types": "'$CRED'"},
                      {"any.types": {"$ne": "__type__"}},
                      {"'$CRED.$CATEGORY'": "work"}]},
  "sort": ["'$CRED.$ROTATED'"], "limit": 50}'
```

```bash
# rotated more than 90 days ago
curl -s -X POST $API/spaces/$SPACE/objects/query -H 'content-type: application/json' -d '{
  "filter": {"'$CRED.$ROTATED'": {"$lt": {"$date": "2026-06-10T00:00:00Z"}}}}'
```

Two things to notice. `{"any.types": "<typeId>"}` is "objects carrying this type". And the `__type__` exclusion: a type definition is itself an object row in the space, and it matches a filter for its own type, so a plain type filter would return the Credential definition among the credentials. Excluding the marker is the idiom; write it every time ([Reading data](../database/reading-data.html)).

The same body against `…/objects/query/subscribe` is a live list — the vault view of your password manager updates as entries change on any device.

## One object, several types

`types` on the create body is a list because an object can carry more than one type. Each carried type adds its own group of columns, keyed by its id, next to the others; none of them knows about the rest. Say the GitHub account is also a paid plan. That is a second type — `subscription`, with a price and a renewal date — attached to the same object:

```bash
SUB=$(curl -s -X POST $API/spaces/$SPACE/types -H 'content-type: application/json' \
  -d '{"name": "Subscription", "xKey": "subscription"}' | jq -r .typeId)
PRICE=$(curl -s -X POST $API/spaces/$SPACE/types/$SUB/properties -H 'content-type: application/json' \
  -d '{"name": "Price", "xKey": "price", "kind": "number", "xFormat": {"type": "currency"}}' | jq -r .propId)
RENEWS=$(curl -s -X POST $API/spaces/$SPACE/types/$SUB/properties -H 'content-type: application/json' \
  -d '{"name": "Renews", "xKey": "renews", "kind": "datetime", "xFormat": {"type": "date"}}' | jq -r .propId)

curl -s -X POST $API/spaces/$SPACE/properties/$GH/attach/$SUB                      # now a credential AND a subscription
curl -s -X POST $API/spaces/$SPACE/properties/$GH/set/$SUB -H 'content-type: application/json' \
  -d '{"patch": {"'$PRICE'": 4, "'$RENEWS'": {"$date": "2026-10-01T00:00:00Z"}}}'
```

Read it back and the row holds both groups, and `any.types` lists both ids:

```json
{ "id": "bafyreig…",
  "any": { "name": "GitHub", "types": ["<credentialTypeId>", "<subscriptionTypeId>"] },
  "<credentialTypeId>":   { "<site>": "https://github.com", "<login>": "ada", "…": "…" },
  "<subscriptionTypeId>": { "<price>": 4, "<renews>": {"$date": "2026-10-01T00:00:00.000Z"} } }
```

The object now answers to both questions: `{"any.types": "'$CRED'"}` lists it among the credentials, `{"any.types": "'$SUB'"}` among the subscriptions, and a sort on `<subscriptionTypeId>.<renews>` puts it in the renewal calendar. Passing both ids under `types` at create does the same in one call; `…/detach/$SUB` takes a type off again.

Three rules make this simple rather than clever:

- **Types coexist; they never inherit from each other.** There is no base type to extend and no chain to resolve. An object is the sum of the types it carries, each contributing its own columns under its own id.
- **Column names never collide.** Each type is its own namespace, so paths are `<typeId>.<propId>` and two types can both have a `name` or a `date` property with the values kept apart. Attaching a second type is opening a second namespace on the object, not merging columns into one flat row.
- **Which type the object "is" is a rendering question, not a data one.** The server stores every group equally. A client that has to pick one layout for the object uses the type's `weight` — [Part 4](apps.html) covers that.

## Change the definition, not the data

A property definition is a synced record like the values. Rename it, reorder it, add an option — none of that touches stored values:

```bash
any type property patch $SPACE $CRED $SECRET --set '{"name": "Password"}'
any type property option set $SPACE $CRED $CATEGORY finance --name Finance --color amber --pos a2
```

A choice value stores the option **key** (`work`), never its label, so renaming an option is one definition write and zero object writes. What you cannot change is `kind`: it is the one thing every peer has already relied on when validating writes (`400 property.immutable`). Details in [Property lifecycle](../database/property-lifecycle.html).

## Where this level ends

You have objects with typed, validated, filterable columns — a schema that syncs with its data — and an object can carry several such schemas at once. It scales to hundreds of credentials, contacts or books: one object per thing, one row each in the objects collection.

It does not scale to a mailbox. Ten thousand emails as ten thousand objects means ten thousand rows in the space-wide collection, each with its own change history, for things that belong together, arrive in bulk and are mostly read as one list. The next part keeps them as **records on a single object** — a dataset with its own enforced schema, ids you supply, and an import that is safe to re-run.

Next: [3. Datasets](datasets.html). Reference: [Types and properties](../database/types-and-properties.html).
