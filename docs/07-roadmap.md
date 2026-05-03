# Roadmap & open questions

## v1 — prototype (this spec)

Goals:
- Start the server (`any run`); auto-wallet on first run.
- Wrap the SDK over HTTP/JSON with the `/v1/` prefix.
- CLI subcommands for every endpoint.
- Make it easy to exercise the SDK from scripts and terminals so we
  can see where the API is sharp / blunt.

Explicit non-goals for v1:
- Subscriptions (see `04-events.md`).
- Remote access / TCP auth.
- Install script / service files.
- Multi-account.
- File upload/download.

## v1.x — what we learn

Whatever the prototype teaches us gets prioritised from here. Likely
candidates based on what's already visible:

- **CLI ergonomics.** Flag shapes, table vs JSON output, patch syntax
  (`--set key=val` vs `--patch FILE`), error messages.
- **Endpoint shape fixes.** Anything that's awkward to call from a
  shell or a binding.
- **Install one-liner.** Ollama-style `curl ... | sh` that drops a
  binary and wires `systemd --user` / `launchctl` to start `any run`
  on login.

## v2 — subscriptions

Most likely WebSocket. Decisions deferred until we have prototype
usage to point at. See `04-events.md` for the tradeoff list.

## v2 — remote access

Once subscriptions land, remote access (LAN or internet) becomes
useful. Needs:

- TCP auth — token stored at `~/.any/token`, `Authorization: Bearer`.
- TLS story — bring-your-own-cert for now, reverse proxy in front.
- Multi-tenant considerations — still single-account per server, but
  clients from different machines.

## Open questions (still unresolved)

1. **Port default.** Picked 7001 arbitrarily. If it collides with
   anything real, change before first ship.
2. **`any init` vs first-`any run` auto-create.** We document both; in
   practice only one needs to exist in v1. Keeping `init` is cheap and
   gives operators a quiet moment to copy the mnemonic — probably keep
   both but revisit if the code grows.
3. **Config file location precedence.** Documented in `05-config.md`.
   Verify `$XDG_CONFIG_HOME/any/config.yaml` is what Linux users
   expect; macOS users might prefer `~/Library/Application Support/any/`.
   Good enough for v1; revisit during packaging.
4. **Passkey UX.** Env-var-only in v1. When we ship the install
   script, we'll need to decide whether the installer pipes the
   passkey to the server start command or integrates with OS
   keychains. Out of v1 scope; flagged now so we don't paint into a
   corner.
5. **Nodeconf path vs inline.** Both supported in the config file.
   Which do we document as the recommended path in README? Probably
   inline for the prototype (self-contained), path for real deploys.
6. **Query response shape.** Baseline `{ "records": [...] }` where
   each record is the `*anyenc.Value` rendered as JSON. Confirm
   anyenc's JSON is a stable wire format — our assumption is yes
   (any-store already treats it that way).
7. **CLI binary vs plugin architecture.** Current plan: monolithic
   `any` binary with subcommands. Plugins are not on the roadmap;
   flag if anyone wants them.
8. **Windows support.** Server + CLI both work in principle (nothing
   Unix-specific since we dropped Unix sockets). Verify during first
   implementation; single-instance lock needs a Windows-friendly
   replacement for the PID-based check.

## SDK-side prerequisites

Not this repo's work; gate on the SDK:

- **ACL public API.** Placeholder today. Handlers return 501 until it
  lands.
- **MembersAPI.** Same.
- **SyncStatusAPI.** Same.
- **`PropertiesAPI.{SetAccount, SetDevice, AttachType, DetachType}`.**
  All return errors today; routes are 501 until the rewrite-object
  (account scope) and device-local store (device scope) ship.
- **`Types.Delete` / type metadata writes / `Types.RemoveProperty` /
  `Types.UpdatePropertyMeta`.** Still "not implemented" on the SDK
  side; routes 501. The frontend currently uses per-device
  `type-meta` overrides for list display names, emoji icons, and local
  hiding, but that is only a UI bridge and must be replaced by real SDK
  methods when available.
- **`Types.Get` for non-object ids.** The SDK only returns
  `space.ErrNotFound` when the id resolves to an existing object that
  isn't tagged as a type. Ids that aren't objects at all surface as a
  wrapped store error ("tree does not exist") and currently fall
  through to `500 internal`. We could widen the 404 mapping in the
  handler if/when the SDK stabilises a sentinel for this case.
- **Query `Projection`.** Accepted in the request body but ignored —
  the SDK's `Projection(opts)` is a no-op in MVP. Update the handler
  once variant collapse and meta-stripping land.

## How to update this file

- Move items that ship to a "Done" section below (or remove them once
  they're obviously in the past).
- Add new questions as they come up during implementation.
- Keep the v1 goal list honest — if we cut something, strike it here
  so a reader knows scope moved.

## Done

- **v1 scaffolding + wallet + health slice** — `cmd/any`, `internal/{cli,server,client,config,api,version}`,
  echo v4 under `/v1`, `GET /v1/health`, `POST /v1/shutdown`, PID-lock with stale
  reclaim, loopback-only bind guard, uniform error envelope, `auth.FileProvider`
  wallet creation with first-run mnemonic print.
- **SDK boot + space lifecycle** — `server.OpenSDK` opens
  `any-sync-sdk` against the wallet provider on Run; nodeconf YAML is
  loaded via `internal/config.LoadNodeconf` (precedence: inline →
  configured path → `../test-etc/staging.yml` fallback). Storage lives at
  `<dataDir>/sdk/`. Real handlers wired:
  - `GET /v1/account` (Id only — Metadata reserved, SDK does not expose it yet)
  - `POST /v1/spaces`, `GET /v1/spaces`, `GET /v1/spaces/:id`, `DELETE /v1/spaces/:id`
    (delete is the SDK's soft-delete — row stays in List with `status:"deleted"`)
  All other `/v1/spaces/**` endpoints from `docs/03-api.md` are
  registered and short-circuit with `501 sdk.not_implemented`, including
  `PUT /v1/account/metadata`, `POST /v1/spaces/{join,derive,one-to-one}`,
  the full Objects/Types/Properties/Members/ACL/Sync-status surface, and
  the data-plane (`query`/`modify`/`delete-records`).
- **Objects + types + properties + data plane** — real handlers wired
  for the SDK surface that's shipped:
  - `POST /v1/spaces/:id/objects`, `POST /v1/spaces/:id/objects/derive`
  - `POST /v1/spaces/:id/modify`, `POST /v1/spaces/:id/delete-records`
    (return `{versionId, changeId, recordIds}` — the full `ModifyResult`)
  - `POST /v1/spaces/:id/types`, `POST /v1/spaces/:id/types/:typeId/properties`
    (PropertyKind allowlist enforced at the boundary: `string` /
    `number` / `boolean` / `null` / `array` / `object` — anything else
    is rejected with 400 `request.schema`)
  - `GET /v1/spaces/:id/properties/:objectId` (with
    `?includeVariants=&includeMeta=` — record rendered via
    `*anyenc.Value.FastJson(arena).MarshalTo` and wrapped in
    `{"record": ...}` as `json.RawMessage`)
  - `POST /v1/spaces/:id/properties/:objectId/base/:typeId`
  Modify / Objects.Create.InitialProperties / Properties.SetBase parse
  request bodies once with a pooled `*fastjson.Parser` and pass
  `*fastjson.Value` directly into the SDK — anyenc converts in one
  walk via `Arena.NewFromFastJson`, no `map[string]any` intermediate.
  Still 501: Objects.Delete, Query, Types.{List,Get,Delete,Properties,
  Remove/UpdateProperty}, Properties.{SetAccount, SetDevice, AttachType,
  DetachType}, all `/members`, `/acl/**`, `/sync-status/**`, and the
  Spaces lifecycle surface that's still SDK-blocked
  (`/spaces/{join,derive,one-to-one}`).
- **Phase 2 endpoints (Objects.Delete, Types read surface, Query)** —
  five more endpoints flipped from 501 to real:
  - `DELETE /v1/spaces/:id/objects/:objectId` — calls `Objects.Delete`,
    returns 204. Any error (including the second-call "tree does not
    exist" response from any-sync) maps to `404 sdk.not_found`.
  - `GET /v1/spaces/:id/types` — `Types.List`. Built-in `any` is always
    first with `builtIn:true`.
  - `GET /v1/spaces/:id/types/:typeId` — `Types.Get`. `space.ErrNotFound`
    maps to `404 sdk.not_found`. The literal id `"any"` returns the
    synthetic built-in.
  - `GET /v1/spaces/:id/types/:typeId/properties` — `Types.Properties`.
    Returns `{properties: [<PropertyDef>, ...]}` with kind rendered as
    a string per the wire allowlist; recursive `items`/`properties`/
    `required` emitted only when the SDK returns them populated.
  - `POST /v1/spaces/:id/query` — chains `Filter / Sort / Limit /
    Offset / All`. Body parsed once with a pooled `*fastjson.Parser`;
    `filter` is passed straight through as `*fastjson.Value`. Each
    record rendered via `value.FastJson(arena).MarshalTo`. The
    `projection` field is **parsed but ignored** — the SDK's
    `Projection(opts)` is a no-op in MVP.
- **Phase 3 endpoint (cross-object query + storage shift)** —
  `POST /v1/spaces/:id/objects/query` wires `Space.QueryObjects()`
  against the per-space shared collection. Same fastjson fast-path as
  Modify; same `{records:[...]}` response shape. `projection` parsed
  but ignored. Storage shift on the SDK side: object property values
  are now one row per object in the shared `objects` collection
  (keyed by objectId), not in a per-object `properties` dataset.
  Type objects appear in the same collection (rows whose `any.types`
  contains `__type__`); filter on that to separate types from
  instances. The existing `/v1/spaces/:id/query` keeps its body shape
  but its useful scope narrowed — it's now mainly for reading a type
  object's `properties` (definitions) dataset, since values moved.
- **Markdown round-trip** — `GET /v1/spaces/:id/objects/:objectId/markdown`
  and `PUT .../markdown` wired against `internal/markdown` (block-tree
  diff against the existing record set, applied as a single `Modify`
  batch). PUT response surfaces per-block `{inserted, updated, deleted,
  unchanged}` counts so the editor can show what landed.
- **Two-step object delete** — `DELETE /v1/spaces/:id/objects/:objectId`
  now tombstones the row in the per-space `objects` collection
  *before* tearing down the any-sync tree (`handlers_objects.go`).
  Required because once the tree is gone, the per-object Modify path
  can't write the tombstone, and queries would keep returning the
  ghost row indefinitely.
- **Nav virtual built-in + tree UI** — `internal/nav` defines a
  synthetic `nav` type (`type` 1=item / 2=folder, `parentId`, `pos`
  via lexid). `POST /v1/spaces/:id/objects` auto-stamps these on every
  create (`injectNavDefaults` — caller-supplied values win, otherwise
  defaults: item, root parent, next-pos after the folder's current max).
  `GET /v1/spaces/:id/types` surfaces `nav` alongside the SDK types so
  the UI can render an editor for it. The web UI's left sidebar is now
  a lazy-loaded tree (queries `nav.parentId` per folder); the space
  picker moved to the right sidebar.
