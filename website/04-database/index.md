---
title: Database
description: Create objects, define their properties, and read or update data on your device.
order: 0
---
# Database

Use the database API to create objects, define their properties, and read or update their content. Queries and writes run on the local Any server. Shared changes sync to the space's members when peers are reachable.

## Choose a task

| I want to… | Start here |
|---|---|
| Create a place to keep shared data | [Spaces](spaces.html) |
| Create a page or another object | [Objects](objects.html) |
| Define what an object is and which fields it has | [Types and properties](types-and-properties.html) |
| Group objects without changing their type | [Collections](collections.html) |
| Find records, sort a list, or load another page | [Reading data](reading-data.html) |
| Change property values or dataset records | [Writing data](writing-data.html) |
| Keep a list current as data changes | [Subscribe](../realtime/subscribe.html) |

New to the model? Read [Data model](data-model.html) first. The distinction that matters most is **one type, any number of collections**.

## How the pieces fit

```text
account
 └── space                         sharing and encryption boundary
      ├── objects                  one row of properties per object
      │     └── object
      │           ├── any.type     exactly one type
      │           ├── any.collections  zero or more collections
      │           └── datasets     blocks, messages, or your own records
      ├── types                    properties, layout, and parts
      └── collections              property definitions
```

A **type** defines an object's layout and **parts**: display units such as a body or a table, backed by datasets. A **collection** adds a group of properties to objects filed under it. Property values are stored at `<ownerId>.<propId>`; the owner is the type or one of the collections.

The built-in `page` and `dataview` types are present in every space. The `miniapp` collection holds the space's app list and pinned objects; `bin` marks objects as moved to the bin. The [catalog](../collaboration/bundles.html#the-usecase-catalog) installs shared definitions such as the wiki collection and general chat.

## Choose the read and write endpoint

| Data | Read | Write |
|---|---|---|
| Object property rows across a space | `POST /v1/spaces/:spaceId/objects/query` | the property set route |
| Records in one object's dataset | `POST /v1/spaces/:spaceId/query` | `/modify`, or the chat/editor module's routes |

Each query has a `/subscribe` endpoint that sends an initial snapshot and subsequent changes. Create returns `objectId`; property and record writes return a receipt with `versionId`, `changeId`, `recordIds` and any `rejections`. Read back through a query when you need the resulting record.

## More guides and reference

| Area | Pages |
|---|---|
| Model and field definitions | [Data model](data-model.html), [Data types](data-types.html), [Property lifecycle](property-lifecycle.html), [System fields](system-fields.html) |
| Your own record schemas | [Runtime datasets](runtime-datasets.html), [Upsert](upsert.html) |
| Query performance and summaries | [Indexes](indexes.html), [Aggregation](aggregation.html) |
| History and document interchange | [Version history](version-history.html), [Markdown import/export](markdown-import-export.html) |
| Shared, deterministic identity | [Derived objects](derived-objects.html) |
