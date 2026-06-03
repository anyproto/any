# Fix: POST /v1/spaces/join returns 400 invite.invalid for a bad token

## Overview
- `POST /v1/spaces/join` currently returns `500 internal` when given an invalid
  invite token. It should return `400 invite.invalid` with a clean message.
- Problem: a malformed token makes the SDK's `Service.Join` fail inside
  `space.DecodeInvite` (wrapping `space.ErrInvalidInvite`). The `spaceJoin`
  handler routes every non-pending error to `aclOpError`, which only matches a
  few ACL substrings, so it falls through to `sdkOpError` → `500 internal`.
- Benefit: callers get an actionable 4xx (CLI maps 4xx → exit 1) instead of a
  misleading server error, and the response stops leaking SDK internals.

## Context (from discovery)
- Files/components involved:
  - `internal/server/handlers_invites.go` — `spaceJoin` (~line 169–214); final
    fall-through is `aclOpError(c, err, nil)` at ~line 211.
  - `internal/server/handlers_acl_common.go` — `inviteDecodeError` helper
    (line 101); currently used only at `handlers_invites.go:194` (pending-path
    re-decode guard).
  - `docs/06-errors.md` — error code namespace list (`invite.invalid` is used
    but undocumented).
- Related patterns found:
  - SDK impl wraps with `%w` (`spaceimpl: Join: %w`), so
    `errors.Is(err, space.ErrInvalidInvite)` works at the `any` layer — no
    fragile substring matching. `space.ErrInvalidInvite` is defined in the SDK
    `space/invite.go`.
  - Existing handlers already use `errors.Is` against `space.ErrNotFound`
    (`handlers_acl_common.go`).
- Dependencies identified:
  - `github.com/anyproto/any-sync-sdk/space` is already imported in
    `handlers_invites.go`; only the stdlib `errors` import needs adding there.
  - SDK source of truth is the **pinned module cache** `any-sync-sdk v0.0.4`
    (`go list -m -f '{{.Dir}}' github.com/anyproto/any-sync-sdk`), not a
    writable `../any-sync-sdk2` sibling as CLAUDE.md's layout suggests — no
    `replace` directive is active. The `%w` wrap chain
    (`spaceimpl/service.go` → `space/invite.go` `ErrInvalidInvite`) is verified
    against that cached copy.

## Development Approach
- **testing approach**: Regular (code first, then tests) — single small, atomic
  change.
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes
- **CRITICAL: all tests must pass before starting next task**
- run tests after each change
- maintain backward compatibility (status codes for the pending-approval and
  RequestJoin paths are unchanged)

## Testing Strategy
- **unit tests**: a new server handler test posting an invalid token and
  asserting `400` + code `invite.invalid`. Uses the existing `newTestDeps`
  harness (boots a real SDK; skips if staging nodeconf absent). The invalid
  token fails inside `DecodeInvite` immediately with **no network call**, so the
  test is fast and deterministic.
- **e2e tests**: none — this project has no UI-based e2e suite. CLI behavior is
  covered transitively (4xx → exit 1) and not separately tested here.

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- keep plan in sync with actual work done

## Solution Overview
- Add a targeted branch in `spaceJoin` that maps `space.ErrInvalidInvite` to the
  existing `inviteDecodeError` helper (400 `invite.invalid`), placed right
  before the final `aclOpError(...)` fall-through.
- Clean up `inviteDecodeError` to emit a **static** message
  (`"invite token is malformed or not recognized"`) instead of echoing the
  wrapped SDK error string. This honors the docs rule against leaking SDK
  internal types/paths and fixes both call sites at once.
- Decision **A** (chosen with user): the check lives in `spaceJoin`, not in the
  shared `aclOpError` helper — invite decoding only happens on the join path, so
  keeping `aclOpError` about ACL semantics avoids a wider blast radius.

## Technical Details
- `errors.Is(err, space.ErrInvalidInvite)` — works because the SDK impl wraps
  the decode error with `%w`.
- `inviteDecodeError` loses its `err` parameter (it no longer formats it). Both
  call sites update:
  - new branch in `spaceJoin` (the bad-token path),
  - existing pending-path guard at `handlers_invites.go:194` (a defensive
    can't-happen case: the token already decoded successfully inside `Join`
    before that branch is reachable).
- Response shape unchanged: `{"error": {"code": "invite.invalid", "message": ...}}`,
  HTTP 400.

## What Goes Where
- **Implementation Steps** (`[ ]`): handler branch, helper cleanup, test, docs.
- **Post-Completion** (no checkboxes): manual CLI smoke check.

## Implementation Steps

### Task 1: Map invalid-invite to 400 in spaceJoin + clean the helper

**Files:**
- Modify: `internal/server/handlers_invites.go`
- Modify: `internal/server/handlers_acl_common.go`

- [x] add `"errors"` to the import block in `handlers_invites.go`
- [x] in `spaceJoin`, immediately before the final `return aclOpError(c, err, nil)`,
      add: `if errors.Is(err, space.ErrInvalidInvite) { return inviteDecodeError(c) }`
- [x] change `inviteDecodeError` in `handlers_acl_common.go` to signature
      `func inviteDecodeError(c echo.Context) error` returning a static
      `400 invite.invalid` with message `"invite token is malformed or not recognized"`;
      remove the `fmt.Sprintf("invite token: %v", err)` (and drop the now-unused
      `fmt` import if nothing else uses it)
- [x] update the existing call site at `handlers_invites.go:194` to
      `inviteDecodeError(c)` (drop the `dErr` argument)
- [x] `go build ./cmd/any` and `go vet ./...` — must pass before writing the test

### Task 2: Test the invalid-token path

**Files:**
- Create: `internal/server/handlers_invites_test.go`

- [x] add a test that POSTs `/v1/spaces/join` with body
      `{"inviteToken": "not-a-valid-token"}` against `newTestDeps`
- [x] assert HTTP status `400` and decoded error code `invite.invalid`
      (success case: the bad token is correctly rejected)
- [x] assert the error `message` contains no SDK internals (no `spaceimpl`,
      no `anysyncsdk`) — turns the no-leak rule into a regression-guarded test
      rather than a one-time manual check
- [x] add an error/edge case: empty `inviteToken` still yields
      `400 request.missing_field` (guards that the new branch did not change the
      earlier validation) — confirms the two 400s stay distinct
- [x] run `go test ./internal/server/` — must pass (or skip cleanly if staging
      nodeconf is absent) before next task

### Task 3: Document the error code

**Files:**
- Modify: `docs/06-errors.md`

- [x] add `invite.invalid                  # 400 — invite token malformed or unrecognized`
      to the error-code namespace list (near the space/object codes)
- [x] no tests (docs-only); re-read the surrounding list to keep formatting
      consistent

### Task 4: Verify acceptance criteria
- [x] `400 invite.invalid` returned for an invalid token (Overview goal met)
- [x] message contains no SDK internals (`spaceimpl`, `anysyncsdk`, paths)
- [x] pending-approval and RequestJoin paths unchanged (out of scope, untouched)
- [x] run full suite: `go build ./cmd/any && go vet ./... && go test ./...`

### Task 5: [Final] Update documentation and close out
- [x] confirm `docs/06-errors.md` updated (Task 3)
- [x] no CLAUDE.md change needed (no new pattern introduced)
- [x] move this plan to `docs/plans/completed/`

## Post-Completion
*Items requiring manual intervention or external systems — informational only*

**Manual verification** (optional):
- Smoke via CLI against a running server:
  `any join --token not-a-valid-token` should print the `invite.invalid` error
  and exit `1` (4xx), not `2` (5xx).

**External system updates**: none — no consuming projects or deployment config
affected.
