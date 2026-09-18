---
title: Networks
description: Which any-sync network a server joins — the embedded production default, staging and self-hosted nodeconfs, the test placeholder — and local-network sync.
order: 40
---
# Networks

A server syncs through an **any-sync network**: coordinator, consensus, sync and file nodes described by a node configuration ("nodeconf"). With nothing configured the binary joins the production network from a configuration embedded at build time; one setting points it anywhere else — choose it before the first boot of a test or private account, because the account pins its network. Independently of that, devices of the same account on one LAN find each other over mDNS and sync directly.

## Choosing a network

| Configuration | Network |
|---|---|
| nothing | **production** — the embedded default, so a packaged install boots from any working directory |
| `network.nodeconfPath: /etc/any/nodeconf.yaml` | the network described by that file (staging, a self-hosted deployment) |
| `network.nodeconf: \|` (inline YAML) | same, inline |
| `ANY_NETWORK_NODECONF_PATH=…` | the environment override |

```bash
ANY_NETWORK_NODECONF_PATH=./staging.yml any run --data-dir ~/any-staging --addr 127.0.0.1:7002
```

```yaml
network:
  nodeconfPath: /etc/any/nodeconf.yaml
```

The embedded servers (`any.aar`, the xcframework) follow the same rule: an empty nodeconf selects the production default, so a mobile host vendors no configuration of its own; non-empty YAML overrides it.

The configuration is read once at startup; changing the file takes effect on the next start. `GET /v1/health` (and `any status`) reports the joined network as `networkId`, before and after sign-in:

```bash
curl -s http://127.0.0.1:7001/v1/health | jq -r .networkId
```

The server returns the id only. Clients recognise the well-known ids and show any other network by a shortened id:

| `networkId` | Network |
|---|---|
| `N83gJpVd9MuNRZAuJLZ7LiMntTThhPc6DtzWWVjb1M3PouVU` | Anytype production (the embedded default) |
| `N9DU6hLkTAbvcpji3TCKPPd3UQWKGyzUxGmgJEyvhByqAjfD` | Anytype stage |

> **Note.** Because the default is production, anything that must not touch real infrastructure — tests, CI, scratch rigs — has to set a nodeconf explicitly. The repository ships a sanitized **placeholder** configuration (the Anytype stage network id with placeholder nodes) that boots and serves but joins no network; tests use it, and it is never selected at runtime.

## What the network provides

- **Sync nodes** replicate encrypted change DAGs between devices and members. They store ciphertext only.
- **The coordinator** answers space membership, invite and deletion questions, and hosts the inbox that surfaces incoming [one-to-one](../collaboration/one-to-one.html) requests. Without a coordinator the inbox notifier is simply off.
- **File nodes** (`fileV2`) back up encrypted file content. Without them, attaching still works offline-first — files sit in the `inflight` durability state until such nodes appear ([Files](../files/status-and-durability.html)).
- **The push node** is *not* part of the nodeconf: it is a direct peer from `push.{peerId, addrs}`, defaulted to the production push node only when the network is production too ([Push](../notifications/push.html)).

`files.publicReadBaseUrl` overrides the public read base the network advertises for file blobs — only for private deployments fronting the object store themselves.

## Self-hosting

A self-hosted any-sync deployment is described by its own nodeconf; point every device's server at it and they form a private network. The push node must be configured separately if you run one.

An account's data belongs to the network that wrote it. The first boot pins that network's id in the account dir's `network.json`, and a server configured for another network refuses the account before touching its dir: `any run` exits naming both ids, `POST /v1/auth` answers `409 auth.network_mismatch` (`details.pinned`, `details.configured`). An account dir without a pin adopts the network it next boots on. If that was the wrong network, or the pin is unreadable (`500 auth.network_pin_corrupt`), remove `network.json` and start the server on the account's network ([Data directory](data-dir.html)).

Keep one data root per network:

```bash
any run --data-dir ~/.any                                          # production
ANY_NETWORK_NODECONF_PATH=./staging.yml any run --data-dir ~/.any-staging --addr 127.0.0.1:7002
```

## Local-network sync (p2p)

Devices of the same account on the same LAN discover each other over mDNS and sync shared spaces directly — including while the sync nodes are unreachable. This is what makes offline LAN sync and a cold restore from a nearby device work.

| Key | Default | Meaning |
|---|---|---|
| `p2p.enabled` | true | false = no listener, no discovery |
| `p2p.port` | 0 | QUIC listen port; 0 = reuse the port persisted from the previous run, or pick an ephemeral one |
| `p2p.serviceName` | "" | mDNS service type, default `_any._tcp`; override to isolate a deployment onto its own discovery namespace |

The state is visible on `any sync-status` (`p2p` / `localPeers` fields) and in detail on the diagnostic snapshot:

```bash
curl -s http://127.0.0.1:7001/v1/debug/p2p
```

```json
{
  "peerId": "12D3Koo…",
  "enabled": true,
  "listenerStarted": true,
  "port": 56187,
  "possibility": "possible",
  "state": "connected",
  "peers": [
    { "peerId": "12D3Koo…", "spaceIds": ["spc_…"], "connected": true }
  ]
}
```

`spaceIds` is the **shared** set only — the exchange proves membership per space and reveals nothing else, so a stranger on the LAN running any-sync shows up with an empty list. A freshly joined space appears once the joiner's read key has synced in.

> **Why it matters.** The network is a relay for ciphertext, not the system of record. Two laptops on a train sync over the LAN with no internet; a phone restores from the laptop next to it; a self-hosted network is a configuration file, not a migration.

## Forcing a sync round

Head sync against the responsible nodes runs on a ~30 s timer. `POST /v1/spaces/:spaceId/sync` (`any space sync <id>`) forces an immediate round and blocks until it completes — useful in scripts and tests that need to collapse a convergence wait ([Sync status](../realtime/sync-status.html)).
