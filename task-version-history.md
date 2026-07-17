# Version history — `any` server plan

Depends on the SDK work in `../any-sync-sdk-history`
(`feat/version-history`, plan in its `task-version-history.md`; proposal:
SDK `docs/version-history-proposal.md` on `worktree-version-history-research`).

## 0. Local co-development wiring

- `go mod edit -replace github.com/anyproto/any-sync-sdk=/home/zarkone/any/any-sync-sdk-history`
  (same mechanism `makefiles/android.mk` uses; drop before merging).

## 1. HTTP surface — `internal/server/handlers_history.go`

Mirror the SDK `HistoryAPI` (proposal §7), following the existing handler
patterns (`handlers_sync_status.go`, `handlers_query.go`):

- `GET /spaces/{spaceId}/objects/{objectId}/history`
  → `ListChanges`; query params: `dataset`, `recordId` (requires
  `dataset`), `traceId`, `author`, `limit`, `cursor`, `coalesce` window.
  Returns `ChangeMeta` rows (version=ChangeId, author, ts, dataset,
  traceIds, touched records, `truncated`, `groupSize`) + next cursor.
- `GET /spaces/{spaceId}/objects/{objectId}/history/{version}`
  → `ViewAt`; historical read-only query over the scratch projection.
  Request-scoped: open view → run the (optional) query params /
  body-supplied query → serialize → `Close()`. No long-lived view
  handles over HTTP in v1.
- `GET /spaces/{spaceId}/objects/{objectId}/history/{version}/datasets/{dataset}/records/{recordId}`
  → `RecordAt` (chat-scale fast path); single record value.
- `GET /spaces/{spaceId}/objects/{objectId}/history/diff`
  → `Diff`; params `base` (empty = per-change effect diff of `version`'s
  PrevIds), `version`, optional `dataset` / `recordIds` scope.
- Space-wide trace listing (index supports `(sp, tr, o)`):
  `GET /spaces/{spaceId}/history?traceId=...` — decide in grooming
  whether v1 or follow-up.

## 2. Semantics & validation (server-owned)

Per our layering rule: SDK validates structure only; the server owns
semantics —

- param validation: limit caps / default page size, cursor opacity,
  `recordId`-requires-`dataset`, coalesce window bounds;
- error mapping: `ErrHistoryTruncated` → 200 + `truncated: true` on the
  oldest entry (not an error), `ErrViewTooLarge` → 413 with "narrow the
  scope" message, `ErrHistoryIndexBuilding` → 202 + progress (or
  Retry-After), unknown version/object → 404;
- `SkipHistory` dataset registration surfaced through the existing
  dataset/schema endpoints (`handlers_datasets.go`) if datasets are
  registered via the server.

## 3. Docs & client surface

- Swagger annotations + regenerate `internal/server/docs`
  (docs.go / swagger.json / swagger.yaml — note: these are currently
  dirty in the main checkout from unrelated work; this worktree is
  clean).
- openapi.go / openapi_mobile.go exposure if history should reach mobile
  bindings in v1.

## 4. Tests

- `handlers_history_test.go` following existing handler-test style:
  - list + pagination + filters (dataset / record / trace / author);
  - coalescing shape (linear chain grouped, branch not grouped);
  - ViewAt returns historical values, excludes local/account scope;
  - RecordAt matches ViewAt for the same version (soundness at the API
    level);
  - diff endpoint: per-change effect diff and base..version diff;
  - stale-index path returns building/progress semantics.

## Sequencing

Server work can start once SDK steps 1–2 land (ListChanges endpoints need
SDK step 4). Suggested order: wire replace + stub routes → ViewAt/Diff →
RecordAt → ListChanges/traces/coalescing → swagger + tests throughout.
