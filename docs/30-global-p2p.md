# 30 — Global p2p

Direct device-to-device sync across the internet, next to the
local-network layer. Two devices that share a space, or two devices of
one account, connect to each other over iroh — QUIC carried by a relay
until the two sides hole-punch a direct path — and sync without a sync
node in the middle.

The SDK owns the layer (`docs/global-p2p.md` and
`docs/account-discovery.md` in `any-sync-sdk`); this file is what `any`
adds: the packaged relays, the configuration block, and the surfaces
that show it working.

## What `any` decides

- **The relays are packaged.** An unconfigured binary joins the
  production network, so it gets the production relays and the pkarr
  relay with it (`internal/config/p2p_global_prod.go`), and the layer
  runs with no config file. A config that names a nodeconf gets relays
  only by naming them — the same pairing push and access use, and what
  keeps staging, local infra, self-hosted networks and every test off
  Anytype's relays.
- **Enablement is derived, not defaulted.** `p2p.global.enabled` absent
  means "on exactly when relay URLs are known". An explicit `false`
  always wins and leaves no iroh endpoint, no UDP socket and no relay
  session.
- **Both layers are independent.** `p2p.enabled: false` with the global
  layer on is a valid setup, and the reverse too.

## Relays

A relay forwards an end-to-end-encrypted QUIC stream between two
devices that cannot reach each other directly, and runs the address
discovery that lets them find a direct path. It has no identity key and
no peer id — clients name it by URL and authenticate it by its ordinary
web certificate — it stores nothing, and relays never talk to each
other, so two devices must share one for a relayed path. A device
probes the configured relays and keeps a session to the nearest.

The pkarr relay is different in kind: it holds one small signed,
encrypted record per account, keyed by a key derived from the account
identity. That record is how this account's own devices find each
other, and how a device holding nothing but the mnemonic finds them.
Without it the account layer stays off and devices know each other only
through the records of the spaces they share.

## The ticket never carries an IP

The endpoint ticket a device publishes into a space's records is
relay-only by construction: it names the device's endpoint id and its
home relay, never its addresses. Direct paths come from relay-mediated
hole punching afterwards, so joining a space never reveals where its
members are. This is why the SDK refuses to start the layer with no
relay configured — the ticket would otherwise have to carry addresses.

## Surfaces

`GET /v1/debug/p2p` returns both layers in one snapshot. The top-level
fields are the LAN layer; `global` carries this device's endpoint id,
its ticket and home relay, whether the relay session is up, every peer
known through records, and the account record's own state (`account`:
relays, sibling device count, whether this device's entry is in it,
last resolve and publish, last error).

Each peer in `global.peers` carries the sources that know it (`lan`,
`global`, `account`), its `lastSeen` and the `tier` derived from it —
`active`, `stale`, `dormant`, `disabled` — which is what sets how often
it is dialed.

`GET /v1/spaces/{spaceId}/sync-status` counts the paths one space is
syncing over right now: `networkPeers` (sync nodes), `localPeers` (LAN)
and `globalPeers` (internet-wide direct), with `p2p` summarizing the
two direct counts as one state.

A space syncing with `networkPeers: 0` and `globalPeers: 1` is the
layer doing its job: no node in the path.

## Reading the numbers

- `global.relayConnected: false` means this device can dial out but
  cannot be reached. A ticket is empty until the session is up.
- `account.enabled: false` means no pkarr relay is configured. Own
  devices are then found only through shared spaces, and a device
  holding only the mnemonic cannot find them at all.
- A peer at tier `disabled` has not been seen for 30 days. Its record
  stays (records are never deleted) and is ignored until its timestamp
  moves.
- `account.clockAheadMs` non-zero means a sibling device's clock runs
  ahead of this one's; the record is signed past it so a slow clock is
  not locked out.

## Cost

The relay session costs about 315 B/min per device whether or not
anything is connected — four relay pings a minute, not tunable. An idle
global connection adds ~170 B/min at the default 60 s keep-alive. A
dial that finds nobody home costs about 13 KB of retransmitted
handshake; the rate limit (6/min, one in flight) and the per-tier
backoff bound that.

While a sync node stream is up, global peers receive nothing: head
updates go through the nodes. A device with no node stream asks its
connected global peers for pushes and becomes their sole path.
