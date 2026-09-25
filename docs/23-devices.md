# Devices registry & active-app election

The account's device registry: one row per device (peer) in the
tech-space system dataset `devices`, plus a deterministic reader-side
election that names at most one **active** device per app slug. This
is how a multi-device account answers "which of my devices runs the
agent" without any coordinator.

## Data model

The `devices` dataset lives on the tech-space index object — the same
derived object that hosts the `spaces` list — and is system-owned: like
the space list, it is not a dataset a client can create in a space, and
writes only go through the restricted endpoints below (never the
generic modify path). Row id = the device's peer id; **all fields are
synced**, so the registry CRDT-replicates to every device of the
account. A raw row (`POST /v1/devices/query`):

```json
{
  "id": "12D3KooW…",                    // peer id (device key identity)
  "name": "workstation",                 // display name
  "os": "linux",                         // runtime.GOOS vocabulary
  "version": "0.9.1",                    // any build version
  "apps": { "bao": { "version": "1.2" } },
  "activeClaims": { "bao": { "seq": 3, "at": 1755450000, "target": "12D3KooWbox…" } }
}
```

`GET /v1/devices` returns the same fields with the row id as `peerId`.

- `apps` is an **open slug set** (same convention as index scopes and
  event types): presence of a slug means "installed on this device"; a
  slug is non-empty and contains no `.`. The per-slug object is a
  free-form scalar bag (string / number / bool — conventionally a
  `version`). Nothing app-specific is baked into `any` or the SDK —
  `bao` is just a slug its runtime uses.
- `activeClaims.<slug>` is the claim this device made for the slug:
  `seq` (writer-supplied monotonic counter — a claim writes `max of all
  visible seqs + 1`, starting at 1), `at` (unix seconds) and `target`,
  the peer id of the device the claim hands the slug to. No `target`
  means the claimer itself. Every device writes only its own row, so
  each claim has exactly one writer.

The server maintains its own row automatically: once the SDK's
bootstrap pass completes, each boot upserts `os` and `version`, and
seeds `name` from the hostname only while the row carries no name (a
user-set name is never clobbered; waiting for bootstrap keeps a
not-yet-caught-up projection from reading as first registration; when
the registry can't be read, only `os` / `version` are written).
Runtimes and UIs only ever add what they own: install flags and
claims.

Online status is deliberately **not** in the dataset — a liveness bit
would churn CRDT history on every heartbeat.

## Election

**Semantics live in the reader, not the write.** Two devices can claim
the same slug concurrently and dataset handlers cannot prevent it; the
rule makes that state survivable by resolving it deterministically at
read time:

> The claims for a slug (`seq` ≥ 1) on all live rows rank by highest
> `seq`, then highest `at`, then lexicographically largest claimer peer
> id. The winner is the target of the best claim whose target is a live
> row carrying the slug under `apps`. The claimer needs no `apps` entry
> of its own.

Properties that follow:

- **Deterministic on converged data**: every reader — either device,
  the UI, a test — computes the same winner. This is why the claim is
  writer-supplied data and NOT a CRDT version id: `_ver` ids are
  peer-locally allocated, so two peers holding the same logical state
  can read different `_ver` values, and an election keyed on them
  would split-brain.
- **Dangling claims never win**: a claim whose target uninstalled the
  app or was pruned is skipped and the next claim decides. Pruning the
  claimer drops its claims with its row. No un-claim write exists, or
  is needed.
- There is no un-claim: the winner changes when a better claim
  appears, or when a claim starts or stops qualifying — its claimer or
  target is pruned, or its target uninstalls or reinstalls the app.
- A device holds one claim per app, so handing the app away replaces
  the device's own claim. Pruning the device that made the winning
  hand-off moves the role back to the best remaining claim, and when a
  hand-off's target stops qualifying, the fallback skips the device
  that handed it away. Both are repaired the same way as any other
  surprise: switch again.

The rule is implemented **once**, in the SDK (`space.ActiveDevice`),
and surfaced pre-resolved as the `active` map on `GET /v1/devices`.
Consumers must read that map rather than reimplementing the rule —
the one real risk in this design is the UI and a runtime computing
different winners from private copies of the logic. A slug has no
`active` entry when no claim names a device that has the app.

## Decision matrix (runtime vs UI)

For an agent runtime keyed on slug `S` (bao's is the reference
consumer):

| Moment | Decider | Action |
|--------|---------|--------|
| Boot; no other row carries `apps.S` | runtime | claim (`POST /v1/devices/activate`) — the majority case, valid even if a dangling claim nominally points elsewhere |
| Boot; `active.S` is another live device | runtime | nothing — second-device install stays passive |
| Active device's row pruned / app uninstalled | runtime (any survivor observing the change) | claim |
| Manual switch | user via UI on any device → `activate {app, peerId}` naming the target | newest claim wins by `seq`; the target's runtime sees itself win and the old winner stands down. Nothing checks that the target runs: a UI offers only targets it sees alive |
| Concurrent claims | every reader, same rule | `(seq, at, peerId)` tiebreak; the loser observes via subscribe and stands down |
| Un-claiming a device | nobody | never happens — only claims, prunes and app installs/uninstalls move the role |
| Claim minted on a stale replica | the client that asked for the switch, after sync | a not-yet-synced replica computes `seq` without the newest claims, so its claim can lose once heads converge (SDK [`docs/tech-space.md`](https://github.com/anyproto/any-sync-sdk/blob/main/docs/tech-space.md) § Devices registry) — observe `active.S` after sync and re-claim if the role didn't land; a one-shot CLI call does not |

The runtime's loop: upsert self (`apps.S`) → read `active.S` +
`self` from `GET /v1/devices` → claim or stand by → watch
`POST /v1/devices/query/subscribe` and react when the winner moves to
or away from `self`. The UI works off the same endpoint and the same
`active` map — no private election code.

## HTTP surface

| Method | Path | Purpose |
|--------|------|---------|
| GET    | `/v1/devices` | `{devices, active, self}` — mapped rows + `active` (per-slug winner peer id) + `self` (this server's peer id) |
| POST   | `/v1/devices/query` | raw windowed snapshot (standard query body, dataset fixed to `devices`; body optional) |
| POST   | `/v1/devices/query/subscribe` | raw windowed live view (SSE, standard frames — `04-events.md`) |
| PUT    | `/v1/devices/me` | `Spaces.SetDevice` — self-row only: `{name?, apps?}`; only present fields are written, `apps` merges per slug, `"apps": {"slug": null}` uninstalls (204) |
| POST   | `/v1/devices/activate` | `Spaces.ClaimActive` — `{app, peerId?}`; writes this device's claim for `peerId`, or for this device when absent; a self claim also self-heals `apps.<app>` (204) |
| DELETE | `/v1/devices/:peerId` | `Spaces.DeleteDevice` — prune a row (204) |

Account-scoped (no `:spaceId`), behind the `/v1` auth guard. The
self-row restriction on `PUT /me` and `activate` is structural, not
checked: the SDK resolves its own peer id for the write, so neither
can touch another device's row. Only `DELETE` writes another row.

`activate` with a `peerId` writes `target` into this device's claim
and never touches `apps`, anywhere. The target must be a row in this
device's registry (`device.not_found`, 404) that carries the app
(`device.app_not_installed`, 409); a device that registered elsewhere
moments ago may not have synced here yet, so both can pass on a
retry. A `peerId` equal to `self` gets the same check: only a claim
with no `peerId` (absent or `null`) marks the app installed. An empty
`peerId` is refused (`request.invalid_field`, 400). A pruned device
gets `device.pruned` whatever it names. Nothing checks that the target
is running: handing the role to a device that is off leaves the app
unanswered until something claims again.

**Deletion is permanent for that peer id.** Record tombstones are
sticky: a pruned device can never re-register — a device that comes
back stays unlisted until it derives fresh peer keys (a new
`any init`). Prune dead devices, not resting ones. The SDK refuses
this server's own row (`device.self_delete`, 400): self-pruning would
permanently lock the installation out — prune it from another device.
Writes from an already-pruned device fail `device.pruned` (409): the
tombstone absorbs them, so `PUT /me` / `activate` can never silently
no-op, and a pruned device cannot move the role.

Errors: `device.not_found` (404, peer id not in this device's registry
— pruned, never registered, or not synced here yet — on DELETE or on
`activate` with a `peerId`); `device.app_not_installed` (409,
`activate` for a device whose row doesn't carry the app here);
`device.self_delete` (400, DELETE of the own row);
`device.pruned` (409, `PUT /me` or `activate` after this device's row
was pruned); `request.invalid_field` (bad slug — empty or containing
`.` — a non-scalar app value, or an empty `peerId`); `request.missing_field` (empty update / missing
app); `request.unknown_field` (unknown body key). See
[errors](06-errors.md).

## CLI

```
any devices list                         # rows + active map + self
any devices register --name laptop --app bao=1.2
any devices register --remove-app bao   # uninstall
any devices activate bao                 # claim on THIS device
any devices activate bao --peer <peerId> # hand the role to another device
any devices remove <peerId> --yes        # permanent prune
any devices query --filter '…'           # raw rows
any devices subscribe                    # raw live stream
```
