---
title: The zen of any
description: The invariants the API keeps — one HTTP surface, reads through query/subscribe, writes through handlers, endpoints 1:1 with the SDK, loopback-only, uniform errors — and the reasoning behind each.
order: 40
---
# The zen of any

The API is small on purpose. Every rule below is something the server refuses to bend, so a client can rely on it without reading the source.

## 1. One surface: HTTP/JSON on loopback

Every caller — the CLI, the web UI, a language binding, an agent — goes through `http://127.0.0.1:7001/v1/…`. Nothing opens the database or talks to sync peers directly. The CLI is a thin client: `any space get X` *is* `GET /v1/spaces/X`, exit code included.

```
0  success      1  user error / 4xx      2  server error / 5xx      3  can't reach server
```

The server refuses to bind anything but a loopback address and there is no auth middleware — the loopback interface is the trust boundary ([Security model](../operations/security-model.html)).

## 2. Reads go through `/query` and `/query/subscribe`

Every dataset — objects, chat messages, editor blocks, files, devices, the space list — is read through the same two POSTs with the same body (`filter` / `sort` / `limit` / `offset` / `includeTotal`) and the same SSE frame set (`ready` → `snapshot` → `changes` → `closed`). One read path per dataset, one wire shape per snapshot.

```bash
curl -X POST http://127.0.0.1:7001/v1/spaces/$SPACE/query \
  -H 'content-type: application/json' \
  -d '{"objectId":"'$OBJ'","dataset":"chat_messages","sort":["-_ver.id"],"limit":50}'
```

The single exception is `GET …/editor/markdown`, which is a render transform, not a dataset read.

## 3. Writes go through the type's handler

Built-in datasets are written only by their bespoke endpoints — chat's send/edit/delete/react, editor's block create/patch/delete — because the handler is what stamps `creator` / `createdAt`, enforces author-only rules, and keys reactions per identity. Generic datasets go through `/modify` (and `/upsert` for id-keyed ingest). Every write returns one shape and never the record body:

```json
{ "versionId": "…", "changeId": "bafy…", "recordIds": ["…"] }
```

Read the result back through a query; that is also where everyone else's writes arrive.

## 4. Endpoints map 1:1 onto SDK methods

If the SDK has it, there is an endpoint. If it does not, there is not — the server does not invent aggregating conveniences. The named exceptions are documented as such: `/search` (a consumer-side index over the change feed), the markdown bridge, the ephemeral `/events` bus, and the bundles registry engine. When a route is registered ahead of its SDK method it returns `501 sdk.not_implemented` rather than pretending.

## 5. Every route lives under `/v1/`

Health, shutdown, OpenAPI — all versioned from day one. Breaking changes are absorbed by bumping the prefix, never by changing a path in place. The full spec is served at `GET /v1/openapi.json`.

## 6. Errors have one shape

```json
{ "error": { "code": "space.not_found", "message": "space bafy… not found", "details": { } } }
```

Same body for every non-2xx. `code` is a dotted, machine-readable handle grouped by area (`auth.*`, `space.*`, `object.*`, `dataset.*`, `markdown.*`, `index.*`, …); `message` is safe to display; `details` is structured context and never contains secrets or filesystem paths ([Errors](../reference/errors.html)).

## 7. POSTs are not idempotent

Each POST produces a new DAG change; retrying a create makes two objects. The one deliberate exception is `POST /v1/spaces/:id/upsert`, where the caller-supplied record id is the idempotency key and an identical re-run diffs to nothing ([Upsert](../database/upsert.html)).

## 8. Bodies are strict, output is pretty JSON

Request schemas are closed: an unknown top-level key is `400 request.unknown_field` naming the accepted set, so a `filters` typo fails loudly instead of querying the whole space. Responses and CLI output are pretty-printed JSON — no table rendering, no `--output` flag.

## 9. Timestamps are instants

Every server-stamped time and every `date` / `datetime` property is `{"$date": "2026-08-05T17:00:00.000Z"}` in both directions, filters included. A bare string or number does not error — it compares by type rank and answers wrong ([Data types](../database/data-types.html)).

## 10. Subscriptions are windows, and `closed` is terminal

A live stream holds a bounded window and delivers deltas to it; on `overflow` or `drifted` the server closes it rather than dropping events, and recovery is always "open a fresh POST and take the new snapshot" — never "replay the gap".

> **Why it matters.** Each of these rules removes a class of client bug. One read path means one parser; one write shape means one place to stamp versions; strict bodies mean typos cannot silently widen a query; terminal `closed` means no client ever reconciles a partial event stream against a snapshot. A hosted backend can afford ad-hoc endpoints because it owns the only copy of the data — a local-first system has as many copies as devices, and consistency of *behaviour* across them is what keeps those copies consistent.

## Further reading

- [Best practices](best-practices.html) — the call patterns built on these rules.
- [HTTP API reference](../reference/http-api.html) — the endpoint catalog.
