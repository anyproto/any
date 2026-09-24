---
title: Devices
description: The account's synced device registry and the deterministic election that names which device runs an app.
order: 20
---
# Devices

Every install of `any` that authorizes the same account is a **device**, identified by its peer id. The account keeps one synced registry of its devices, and a deterministic reader-side election answers "which of my devices runs the agent" with no coordinator.

## The registry

The `devices` dataset lives in the account's tech space — the same place the space list lives — with one row per device, row id = peer id. All fields are synced, so the registry replicates to every device of the account and is invisible to other accounts. A raw row, as `POST /v1/devices/query` returns it:

```json
{
  "id": "12D3KooW…",
  "name": "workstation",
  "os": "linux",
  "version": "0.9.1",
  "apps": { "bao": { "version": "1.2" } },
  "activeClaims": { "bao": { "seq": 3, "at": 1755450000 } }
}
```

| Field | Written by | Meaning |
|-------|------------|---------|
| `name` | client (`PUT /v1/devices/me`); seeded from the hostname on first registration | display name — a user-set name is never clobbered |
| `os`, `version` | the server, on every boot | `runtime.GOOS` vocabulary and the `any` build |
| `apps` | client | open slug set; presence means "installed here"; the value is a scalar bag, conventionally `{version}` |
| `activeClaims.<slug>` | `POST /v1/devices/activate` | the claim for this device to be the slug's active instance (written by this device, or by another one handing it the role): `seq` (max of all visible seqs + 1) and `at` (unix seconds) |

Online status is deliberately not a field — a liveness bit would churn CRDT history on every heartbeat.

## Election

Two devices can claim the same slug concurrently; the design makes that survivable by resolving it at read time:

> Candidates for a slug are the live rows that carry the slug under `apps` **and** hold a claim for it. The winner is the candidate with the highest `seq`; ties fall to the highest `at`, then to the lexicographically largest peer id.

Consequences:

- **Deterministic on converged data.** Every reader computes the same winner. The claim is writer-supplied data, not a CRDT version id — version ids are allocated per peer and would split-brain an election.
- **Dangling claims never win.** Uninstalling an app (or pruning a row) forfeits its claim; there is no un-claim write.
- **No un-claim.** The role moves only through a newer claim (for any device) or the winner's row losing the app.

The rule is implemented once, inside the SDK, and returned pre-resolved as the `active` map on `GET /v1/devices`. Read that map; do not reimplement the rule.

> **Note.** A claim minted on a replica that has not synced the newest claims can lose once heads converge. After sync, observe `active.<slug>` and re-claim if the role did not land.

## HTTP surface

Account-scoped, behind the `/v1` auth guard.

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/v1/devices` | mapped rows (the row id as `peerId`) + `active` (per-slug winner) + `self` (this server's peer id) |
| POST | `/v1/devices/query` | raw windowed snapshot, standard [query body](../database/reading-data.html) |
| POST | `/v1/devices/query/subscribe` | raw live view over SSE, standard [frames](../realtime/subscribe.html) |
| PUT | `/v1/devices/me` | self row only: `{name?, apps?}`; `"apps": {"slug": null}` uninstalls → `204` |
| POST | `/v1/devices/activate` | `{app, peerId?}` — claim the slug on the device `peerId` names, or on this device when absent; a self claim also self-heals `apps.<app>` → `204` |
| DELETE | `/v1/devices/:peerId` | prune a row → `204` |

```bash
curl -s -X PUT http://127.0.0.1:7001/v1/devices/me \
  -d '{"name": "laptop", "apps": {"bao": {"version": "1.2"}}}'

curl -s -X POST http://127.0.0.1:7001/v1/devices/activate -d '{"app": "bao"}'

# hand the role to another of the account's devices
curl -s -X POST http://127.0.0.1:7001/v1/devices/activate -d '{"app": "bao", "peerId": "12D3KooW…"}'

curl -s http://127.0.0.1:7001/v1/devices
```

```json
{ "devices": [ { "peerId": "12D3KooW…", "name": "laptop", "apps": { "bao": { "version": "1.2" } }, "…": "…" } ],
  "active": { "bao": "12D3KooW…" },
  "self": "12D3KooW…" }
```

```bash
any devices list
any devices register --name laptop --app bao=1.2
any devices register --remove-app bao
any devices activate bao
any devices activate bao --peer <peerId>
any devices remove <peerId> --yes
any devices subscribe
```

`PUT /me` is self-row only by construction: the SDK resolves its own peer id for the write. `activate` with a `peerId` is the one write that lands on another device's row, and it writes only `activeClaims.<app>` there — never `apps`, since the claimer can't know what the target has installed. The target must be a live row that already carries the app.

## A runtime's loop

For an agent runtime keyed on slug `S`:

| Moment | Action |
|--------|--------|
| Boot; no other row carries `apps.S` | claim — the majority case, valid even if a dangling claim points elsewhere |
| Boot; `active.S` is another live device | nothing — a second install stays passive |
| Active device pruned or app uninstalled | any survivor observing the change claims |
| Manual switch | the user, on any device, activates with the target's `peerId`; the newest claim wins by `seq` |
| Concurrent claims | every reader applies the same tiebreak; the loser sees it on subscribe and stands down |

Upsert self (`apps.S`) → read `active.S` and `self` → claim or stand by → watch `POST /v1/devices/query/subscribe` and react when the winner moves to or away from `self`. A UI works off the same endpoint and the same `active` map. The [scheduling section](../scheduling/device-pins.html) builds on this election.

## Pruning is permanent

Record tombstones are sticky: a pruned peer id can never re-register. A device that comes back stays unlisted until it runs with a fresh device key — a new `any init` on a standalone server, a removed `device.key` on a managed one. Prune dead devices, not resting ones — and prune from another device: the SDK refuses to delete its own row.

| Status | Code | When |
|--------|------|------|
| 404 | `device.not_found` | unknown or pruned peer id on DELETE or on `activate` with a `peerId` |
| 400 | `device.self_delete` | DELETE of this server's own row |
| 409 | `device.pruned` | a self-row write after the row was pruned |
| 409 | `device.app_not_installed` | `activate` with a `peerId` whose row doesn't carry the app |
| 400 | `request.invalid_field` | bad slug, or a non-scalar app value |
| 400 | `request.missing_field` | empty update, or `activate` without `app` |
