# SDK drift — `any-sync-sdk` docs/comments vs. observed v0.0.4 behavior

A living list of places where the SDK's own documentation, comments, or
doc-comments disagree with how it actually behaves (as compiled into this
server: `github.com/anyproto/any-sync-sdk v0.0.4`, `any-store v0.4.6`). Each
entry is something to either work around here or fix upstream. Verified by
probing the live server unless noted.

Paths below are inside the module cache:
`$(go env GOMODCACHE)/github.com/anyproto/any-sync-sdk@v0.0.4/`.

---

## 1. `IncludeTotal` is page-bounded, not unbounded

- **Doc claims:** `space/query.go:79-84` — "IncludeTotal asks for a one-shot
  count of filter-matching records **(independent of limit/offset)**." And
  `internal/spaceimpl/query.go:186-188` — "Count ignores them [limit/offset]."
- **Actual:** `Snapshot` computes the total via `q.Count`, which calls
  `q.build(coll)` — and `build` *does* apply `out.Limit(q.limit)` /
  `out.Offset(q.offset)` (`internal/spaceimpl/query.go:446-449`). So the count
  is taken on the limited query.
- **Observed:** `objects/query` with `limit:2, includeTotal:true` over 3
  matching objects returns `total:2`; without a limit, `total:3`.
- **Impact:** you cannot get an unbounded match count alongside a page. For an
  exact total today, query without `limit` (full scan).
- **Fix:** `Count` should build without limit/offset (or `Snapshot` should pass
  a limit/offset-cleared builder to `Count`).

## 2. `Snapshot` carries a stale "stub" comment

- **Comment:** `internal/spaceimpl/query.go:177-178` — "Snapshot returns a
  point-in-time view plus optional total count. **Stub for the engine
  integration — filled in by task 4.**"
- **Actual:** it is fully implemented (queries, clones results, computes total).
  The "stub / task 4" wording is leftover and misleading.

## 3. Property `propId` vs `xKey` — easy to misread

- Not contradictory, but a sharp edge worth recording. `Types().AddProperty`
  (`internal/spaceimpl/types.go:104-147`) derives the returned **propId** from
  the change CID (`Upsert: true // empty Id → propId derived from ChangeId`);
  the `xKey` you pass is a *separate stable field* on the definition.
- **Consequence:** property values are stored/validated at
  `record[typeId][propId]`, and **writes must key by propId**, not xKey.
  Writing by xKey → `400 property.not_found`. `GET /types/:id/properties`
  returns `{id, name, xKey, kind}` so callers can map xKey→propId. anyHelper
  hides this behind dotted `"<typeXKey>.<propXKey>"` resolution.
- **Types now have an xKey too** (SDK branch `add-type-xkey`): a record's per-type
  root keys and `any.types` entries are **type ids** (CIDs for user types). The
  type xKey is *separate* stable metadata (`any.xkey` on the type object,
  returned in `TypeInfo.XKey`); anyHelper keys read records by xKey and resolves
  dotted type segments xKey/name/id → typeId. ⚠ The branch's `Create` writes
  `any.xkey` but the `any` builtin schema didn't declare an `xkey` property — we
  added it (`internal/types/any/any.go`); incorporate into the PR. There's no
  API to set/rename a type's xKey after create (first-create-wins), so existing
  pre-xKey types can't be backfilled — recreate (fresh space) to get one.

## 4. (Repo) the sibling-checkout `replace` — removed by the merge, re-added for xKey

- The `main` merge dropped the `replace` and pinned published
  `any-sync-sdk v0.0.4`. To pick up the type-xKey work, `go.mod` now re-adds
  `replace github.com/anyproto/any-sync-sdk => ../any-sync-sdk2` (branch
  `add-type-xkey`, which is `v0.0.4` + the xKey commit + the `any.xkey` fix).
  Read `../any-sync-sdk2` (on that branch), not the v0.0.4 module cache, when
  checking SDK behavior now.

## 5. (Runtime) the stock `anytype-agent-runtime` loader targets anytype-heart, not `any`

- **Claim:** the CLI help says `import "name@v1"` resolves "via the Anytype
  API", implying any Anytype-compatible server.
- **Actual:** the stock loader (`anyruntime/anytype_loader.go`) hits
  `GET /v1/spaces/:id/objects` and reads `__anytype_program_name` — that's
  anytype-heart's API shape, which the `any` server does not serve (it uses
  `POST /objects/query` and per-type propIds). So against `any`, the stock
  loader resolves nothing and falls through to the `-m` file loader.
- **Consequence:** bobrik-watch wires its OWN `any`-speaking resolver
  (`cmd/bobrik-watch/runtime.go` → `newAnySDKLoader`), so program imports work
  in production. But JS-harness tests that exec the stock CLI cannot exercise
  module-import paths (e.g. anyPrograms `_verifyImportable`) for programs stored
  in `any` — only file-loaded (`-m`) modules resolve. Tests assert the save and
  treat the live-import probe as a known harness gap.

---

How to add an entry: probe the live server (or read the v0.0.4 source in the
module cache), state what the doc/comment claims, what actually happens, the
observable repro, and the suggested fix.
