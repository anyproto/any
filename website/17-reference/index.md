---
title: API & configuration
description: Look up HTTP endpoints, CLI commands, stream frames, errors, configuration and runtime contracts.
order: 0
---
# Reference

Use this section when you know what you want to do and need the exact request, response or setting. For an end-to-end introduction, start with [your first app](../quickstart/curl.html).

## Database server

| Look up | Contents |
|---|---|
| [HTTP API](http-api.html) | Endpoints, request bodies, responses and operation-specific errors. |
| [CLI](cli.html) | `any` commands, flags, input formats and exit codes. |
| [SSE streams](events.html) | Subscription frames, snapshots, deltas and close reasons. |
| [Errors](errors.html) | HTTP statuses and machine-readable error codes. |
| [Server configuration](config.html) | Configuration keys, defaults, environment variables and flags. |

## Companion runtime

| Look up | Contents |
|---|---|
| [anyrt CLI](anyrt-cli.html) | Run, serve, deploy, tracing and the runtime control API. |
| [Effects catalog](effects-catalog.html) | Operations available to programs through the effect boundary. |
| [Trigger schema](trigger-schema.html) | Schedules, device ownership and execution state. |
| [anybao.toml](anybao-toml.html) | Runtime configuration and provider setup. |

The standalone setup runs Any and anyrt as separate processes; application hosts can embed the server or runtime. The Any database API normally listens on `127.0.0.1:7001`; the anyrt control API normally listens on `127.0.0.1:7010`. The `any` CLI calls the database API. anyrt also uses that API when it works with your data.

## Read the examples correctly

- **Base URL:** database routes use `http://127.0.0.1:7001/v1`. In endpoint tables, `…` abbreviates the prefix identified by that table.
- **IDs:** `$SPACE` or `$SP` means a space ID; `$OBJ` means an object ID; `$CHAT` means the general chat’s object ID. Set them to values from your own setup. An object ID, a type ID and a property ID are different values.
- **Property keys:** custom values use `propId`. Resolve an `xKey` to its property ID before writing.
- **Dates:** instants use `{"$date": "2026-08-05T17:00:00.000Z"}`. A bare string is a different value type.
- **Write receipts:** many writes return `versionId`, `changeId` and `recordIds`, rather than the updated record. Read the record through a query or subscription.
- **Errors:** non-2xx responses use `{"error": {"code", "message", "details?"}}`. Some batch operations report individual rejections inside a successful HTTP response; check that endpoint’s contract.

Definitions of the shared vocabulary are in the [glossary](glossary.html). For a particular operation, its endpoint entry is the authoritative request and response shape.
