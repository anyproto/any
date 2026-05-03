# PR #16 — Hide soft-deleted spaces

> Bug fix: deleting a space made the row vanish locally but it
> reappeared on the next refetch. Root cause is in the SDK + server,
> not the UI.

## Symptom

User clicks "Delete space" → toast says success → row removed
optimistically → React Query invalidates the spaces list → refetch →
**deleted space reappears**.

## Root cause

The SDK's `Spaces().Delete(ctx, spaceId)` is a soft-delete — it
sets `LocalStatus = StatusDeleted` on the techspace index entry. The
underlying CRDT data is preserved so the action is recoverable on
the server side. **However:**

- `Spaces().List(ctx)` returns every index row regardless of status,
  with `Status` set to `space.StatusDeleted` for soft-deleted ones.
- Our `spaceList` handler maps every entry into the response without
  filtering.
- `spaceGet` likewise returns the metadata of a deleted space.

So the client correctly invalidates, correctly refetches, and the
server correctly returns the soft-deleted record. The bug is that
the server was leaking soft-deleted entries to clients that have no
way to act on them.

## Decision

Filter at the server, not the client. Two reasons:
1. The CLI hits the same endpoints — fixing in the UI alone would
   leave `any spaces list` showing zombies.
2. The "show deleted spaces" feature (recover / purge) is a future
   admin surface, not a default. We add `?include=deleted` later if
   needed.

Specifically:

- `GET /v1/spaces` — drop entries with `Status == space.StatusDeleted`
  before returning the list.
- `GET /v1/spaces/:spaceId` — if the space's `Status` is
  `StatusDeleted`, return **404 `space.not_found`** instead of the
  metadata. The client already handles 404 → invalidate.
- `DELETE /v1/spaces/:spaceId` — unchanged. Already returns 204.

Hard-delete (purge from disk) is a separate, riskier operation we
defer until the SDK exposes it. Tracked as a follow-up.

## Files

```
docs/specs/PR-016-hide-deleted-spaces.md
internal/server/handlers_spaces.go       (filter list; 404 on get)
internal/server/handlers_spaces_test.go  (new — list filter + get 404)
```

## Acceptance criteria

1. Deleting a space → it does not reappear on a refetch.
2. `GET /v1/spaces/:id` for a deleted space returns 404 with
   `{"error": {"code": "space.not_found"}}`.
3. `GET /v1/spaces` returns only `Active` / `Joining` /
   `Leaving` / `RemoteDead` spaces.
4. CLI `any spaces list` no longer shows deleted spaces.
5. Existing tests stay green; new tests cover the list filter and
   the get-on-deleted 404.

## Test plan

- **Unit**: stub `SpacesService` returning a mix of statuses; assert
  `spaceList` drops the deleted one.
- **Unit**: `spaceGet` on a deleted-status space → 404 envelope.

## Follow-ups (not in this PR)

- `?include=deleted` query param to surface the trash for a future
  recover / hard-delete UI.
- `POST /v1/spaces/:id/restore` once the SDK exposes the inverse of
  `SetLocalStatus(StatusDeleted)`.
- Hard-delete (`POST /v1/spaces/:id/purge`) when the SDK supports it.
