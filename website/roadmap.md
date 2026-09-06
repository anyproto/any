---
title: Roadmap
description: What any ships today, what is planned next, and the open questions still being decided — in user-facing terms.
order: 0
---
# Roadmap

any is a local-first database with a stable `/v1/` HTTP surface and a growing set of modules, collaboration primitives and a sandboxed runtime. This page tracks the surface at the feature level: what you can rely on now, what is next, and where the design is still open.

## Shipped

| Area | What you can use today |
|---|---|
| Server and accounts | `any run` on loopback, mnemonic-restored accounts with fresh device keys, per-account data dirs, `POST /v1/auth` onboarding, the OpenAPI 3.1 document at `/v1/openapi.json` — [Accounts](auth/accounts.html) |
| Data plane | objects, types and properties with descriptors and choice options, Mongo-style query with windowed subscribe over SSE, aggregation pipelines, version history, runtime dataset schemas with idempotent upsert, datetime instants, derived `createdAt`/`modifiedAt`/`modifiedBy`/`author` — [Database](database/index.html) |
| Modules | chat with reactions, mentions, threads, attachments and private read tracking; the block editor with a lossless markdown bridge and surgical text edits — both served to any type whose part declares them; type parts, weight and layout; `any://` links — [Types](types/index.html) |
| Collaboration | members, invites, guest keys, ACL operations, direct-add by identity, one-to-one spaces, well-known derived spaces, bundles with derived roots, the encrypted identities directory — [Collaboration](collaboration/index.html) |
| Files | encrypted content-addressed files with offline-first backup, ranged download, variants, pin/offload/retry, account-wide cache control — [Files](files/index.html) |
| Realtime | windowed query/subscribe, live space list, sync status, the account-wide event bus with device/account/space scopes, the process helper, LAN peer discovery and sync — [Realtime](realtime/index.html) |
| Search | local BM25 full-text plus vector search with hybrid ranking; local, Ollama and OpenAI-compatible embedders; rebuild on engine version bumps — [Search](search/index.html) |
| Devices and push | device registry with active-app election, mobile push for chat with end-to-end encrypted payloads and per-space / per-chat notify modes — [Devices](auth/devices.html), [Push](notifications/push.html) |
| Programs and agents | the anyrt sandbox with a recorded effect boundary and bit-exact replay, space-resident programs and skills via overlays, cron / once / event triggers with device pins, managed OAuth credentials — [Programs](programs/index.html), [Scheduling](scheduling/index.html), [Agents](agents/index.html) |
| Platforms | desktop tarballs for four platforms, an Android `.aar` and an iOS xcframework built from one CI pipeline — [Builds and CI](operations/builds-and-ci.html) |

## Planned

**Near term — shaped by use.** The API is exercised daily by the desktop app, the mobile shells and the agent runtime, and the next round of work follows what they hit:

- **CLI ergonomics** — flag shapes, patch syntax, error messages; a handful of endpoints (space create/list, snapshot query, modify, delete-records, bundles) still need their `any` subcommand.
- **Endpoint shape fixes** — anything awkward to call from a shell or a language binding, absorbed by bumping the `/v1/` path rather than breaking routes in place.
- **Install one-liner** — a `curl … | sh` that drops the binary and wires `systemd --user` / `launchctl` to start `any run` at login.
- **Projection push-down** — `projection` is applied at the serialization boundary today; pushing the field set into the store's find path would cut the document decode too.
- **Runtime type binding** — attach/detach a type on an existing object (bind at create for now).
- **Account-scope record fields** — dataset fields declared `account` are readable but not yet writable; property values already are.

**Search.** Backfill / full re-index of content written before indexing was on; snippets and highlighting; per-scope weights and cross-space search; re-embedding on model change; splitting long records into several chunks for vector recall.

**Chat.** Push for reactions and ACL/invite events; desktop receive.

**Realtime.** A soft `lagged` signal on windowed subscribes for clients that want to ride out bursts without resubscribing; resume from a version cursor once the engine supports replay; a WebSocket multiplex (`/v2/subscribe`) if a consumer needs many subscriptions per connection — additive, SSE stays.

**Remote access.** Today the server binds loopback only and the trust boundary is the machine. The remote story adds bearer-token auth, a bring-your-own-cert TLS option behind a reverse proxy, and clients from several machines on one account.

**Runtime.** The `event` trigger kind's `filter` field; per-file search indexing; the schema-driven handler replacing the last compiled-in dataset handlers where no cross-field rule needs code.

## What stays stable

| Surface | Commitment |
|---|---|
| `/v1/` paths and body shapes | additive changes only; breaking changes come with a prefix bump |
| Error envelope and code namespace | new codes are added, existing ones keep their meaning — [Errors](reference/errors.html) |
| SSE envelope and close reasons | one reason set across every stream — [SSE streams](reference/events.html) |
| Data on disk | your data dir is the database; the sync network relays ciphertext only |
| Debug endpoints (`/debug/**`) | explicitly unstable — fields may move with the engine |

## Open questions

- **Default port.** `7001` was chosen arbitrarily; it changes before first packaged release if it collides with anything common.
- **Config file location on macOS.** `$XDG_CONFIG_HOME/any/config.yaml` is what Linux users expect; macOS users may prefer `~/Library/Application Support/any/` — revisited during packaging.
- **Passkey UX.** The wallet passkey arrives by environment variable or stdin. Whether an installer pipes it in or integrates with OS keychains is decided with the install script.
- **Strict request bodies.** `/modify` and `/aggregate` hand their op and stage vocabularies straight to the engine and drop unknown keys silently; a strict gate waits for the engine to export that grammar.
- **One macOS build.** The App-Sandbox-safe `-sandbox` variant is the strictly more conservative one; the end state is a single darwin build using it once the oldest supported macOS is verified.

> **Note.** Breaking changes to request and response shapes are absorbed by the `/v1/` prefix: the next iteration bumps the path rather than changing a route under you. Pre-release engine upgrades can reset local state — see [Data dir](operations/data-dir.html) for what to back up.
