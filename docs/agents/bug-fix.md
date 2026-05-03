# Playbook: Fix a bug

The order matters: **reproducer test first, then the fix**. Don't
ship a fix without a test that fails before the fix and passes after.

## Steps

1. **Confirm the bug locally.** Reproduce it on `main`. If you can't
   reproduce, the bug is either fixed or under-described — report
   that back instead of guessing.

2. **Write a failing test.** Place the test next to the code that
   misbehaves:

   - Pure logic → `*.test.ts` next to the function.
   - Component behavior → `*.test.tsx` next to the component.
   - Server route → `internal/server/*_test.go`.
   - End-to-end flow → `e2e/<feature>.spec.ts`.

   Run the test and confirm it fails for the *expected* reason. If
   it fails for the wrong reason, the test isn't actually about the
   bug yet.

3. **Fix the root cause, not the symptom.** Read the code path that
   produces the bug. Fix the smallest scope that resolves all three
   of: the failing test, the original repro, and any closely related
   cases.

4. **Run the gates locally.**
   - `pnpm -C web/app lint` (no new violations).
   - `pnpm -C web/app typecheck` (clean).
   - `pnpm -C web/app test:run` (all green).
   - For server changes: `go vet ./...` and `go test ./...`.
   - For UI changes that altered visuals: `pnpm -C web/app test:e2e`
     and update the snapshot if the new pixels are correct.

5. **Write the commit / PR.** Title: `fix(<area>): <short description>`.
   Body: include
   - the symptom,
   - the root cause,
   - the test that exercises it,
   - any follow-up issues.

## Anti-patterns

- ❌ Fix the symptom (e.g. swallow the error) without understanding
  the root cause.
- ❌ Add a test that passes both before and after the change.
- ❌ "Defensive" code paths added "just in case" — they hide the
  next bug. Add them only when there's a concrete reason.
- ❌ Wide refactors smuggled into a fix PR. Split them.
- ❌ Updating a snapshot file without explaining the new pixels.
