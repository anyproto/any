# SDK drift — `any-sync-sdk` docs/comments vs. observed behavior

A living list of places where the SDK's own documentation, comments, or
doc-comments disagree with how it actually behaves (as compiled into this
server: `any-sync-sdk` pinned at the tagged release `v0.0.8` — with
`any-store/v2 v2.0.0-alpha.10`). Each entry is something to either work
around here or fix upstream. Verified by probing the live server unless
noted.

Read the SDK source in the module cache
(`$(go env GOMODCACHE)/github.com/anyproto/any-sync-sdk@<version>/`).

---

## 1. Property `propId` vs `xKey` — easy to misread

- Not contradictory, but a sharp edge worth recording. `Types().AddProperty`
  (`internal/spaceimpl/types.go`) derives the returned **propId** from
  the change CID (`Upsert: true // empty Id → propId derived from ChangeId`);
  the `xKey` you pass is a *separate stable field* on the definition.
- **Consequence:** property values are stored/validated at
  `record[typeId][propId]`, and **writes must key by propId**, not xKey.
  Writing by xKey → `400 property.not_found`. `GET /types/:id/properties`
  returns `{id, name, xKey, kind}` so callers can map xKey→propId. anyHelper
  hides this behind dotted `"<typeXKey>.<propXKey>"` resolution.
- **Types have an xKey too:** a record's per-type root keys and `any.types`
  entries are **type ids** (CIDs for user types). The type xKey is *separate*
  stable metadata (`any.xkey` on the type object — now declared by the SDK
  builtin `any` schema, `internal/types/any/any.go`; returned in
  `TypeInfo.XKey`); anyHelper keys read records by xKey and resolves dotted
  type segments xKey/name/id → typeId. There's no API to set/rename a type's
  xKey after create (first-create-wins), so pre-xKey types can't be
  backfilled — recreate (fresh space) to get one.

## 2. (Runtime) the stock `anytype-agent-runtime` loader targets anytype-heart, not `any`

- **Claim:** the CLI help says `import "name@v1"` resolves "via the Anytype
  API", implying any Anytype-compatible server.
- **Actual:** the stock loader (`anyruntime/anytype_loader.go`) hits
  `GET /v1/spaces/:id/objects` and reads `__anytype_program_name` — that's
  anytype-heart's API shape, which the `any` server does not serve (it uses
  `POST /objects/query` and per-type propIds). So against `any`, the stock
  loader resolves nothing and falls through to the `-m` file loader.
- **Consequence:** the `any`-speaking resolver lives in
  `internal/anyrt` (`NewAnySDKLoader`, chained via `anyruntime.ChainLoaders`
  in `internal/anyrt/runtime.go`), so program imports work in production. But
  JS-harness tests that exec the stock CLI cannot exercise module-import
  paths (e.g. anyPrograms `_verifyImportable`) for programs stored in `any` —
  only file-loaded (`-m`) modules resolve. Tests assert the save and treat
  the live-import probe as a known harness gap.

---

Resolved upstream since the v0.0.4 baseline (removed from this list):
`IncludeTotal` is now unbounded (`Snapshot` counts via `totalWithin`,
ignoring limit/offset, and adds `HasNext` — SDK `fd4c769`); the `Snapshot`
"stub / task 4" comment is gone; the builtin `any` type now declares the
`xkey` property; and `any` imports the published `any-sync-sdk` module.

How to add an entry: probe the live server (or read the SDK source in the
module cache), state what the doc/comment claims, what actually happens,
the observable repro, and the suggested fix.
