---
title: Networks
description: Choose a sync network and data root, initialize an isolated account, and understand what the placeholder does and does not disable.
order: 90
---
# Networks

Choose which sync network the server uses before its first run. With nothing configured, Any joins production. A separate network needs its own data root and an account initialized in that root.

The local HTTP address and the sync network are different settings. A *nodeconf* YAML describes the coordinator, tree nodes, file nodes, and their addresses. It does not select an embedding or language-model provider.

## The nodeconf

```yaml
networkId: N9DU6hLkTAbvcpji3TCKPPd3UQWKGyzUxGmgJEyvhByqAjfD
nodes:
  - peerId: 12D3Koo…
    addresses: [ "node1.example.net:6666", "quic://node1.example.net:8888" ]
    types: [ tree ]
  - peerId: 12D3Koo…
    addresses: [ "file1.example.net:6666" ]
    types: [ file, fileV2 ]
  - peerId: 12D3Koo…
    addresses: [ "coord1.example.net:6666" ]
    types: [ coordinator ]
```

| Node type | Role |
|-----------|------|
| `coordinator` | Space registry, ACL/invite coordination, deletion, the 1-1 inbox |
| `consensus` | Orders each space's ACL log — membership, invites, permissions |
| `tree` | Stores and relays encrypted object-tree changes; the peers head-sync talks to. Tree nodes that run the pub/sub relay also carry account- and space-scope [events](../realtime/event-bus.html) |
| `file` / `fileV2` | File byte storage. Durable file backup needs `fileV2` nodes; without them attaches stay `inflight` |

The `networkId` is part of every space's identity — a space created on one network cannot be moved to another.

## Choosing one

Precedence is config file → env → flag:

```yaml
# config.yaml
network:
  nodeconfPath: /etc/any/nodeconf.yaml
  # or inline:
  # nodeconf: |
  #   networkId: …
```

```bash
ANY_NETWORK_NODECONF_PATH=/etc/any/staging.yaml any run
```

| You want | Do |
|----------|----|
| **Production** — real data, real peers, other devices of your account | Configure nothing. The production nodeconf is compiled into the binary, so a packaged install boots from any working directory. |
| **Staging** — the same topology on throwaway infrastructure | Point `network.nodeconfPath` / `ANY_NETWORK_NODECONF_PATH` at the staging nodeconf. |
| **Local infra** — your own any-sync nodes on a LAN or in containers | Same knob, your own YAML. Everything works offline-first regardless, so partial deployments (no `fileV2`, no event relay) degrade feature by feature, never at boot. |
| **Unreachable sync nodes** — tests or a single-machine demo | Use the sanitized **placeholder** below; LAN discovery and model downloads are separate settings. |

> **Note.** The production default is a convenience for installs, and a trap for experiments: a test script that forgets to set the env var creates real spaces on the real network under your real account. Set `ANY_NETWORK_NODECONF_PATH` in the shell you develop in, or put `network.nodeconfPath` in a per-experiment `config.yaml` passed with `--config`.

## The placeholder: a local sandbox

The source checkout includes `internal/config/nodeconf-placeholder.yml`, with a real `networkId` and fake configured peers. It cannot connect to those sync nodes. Start from the repository root and initialize an account in the same dedicated root you will run:

```bash
export ANY_NETWORK_NODECONF_PATH="$PWD/internal/config/nodeconf-placeholder.yml"
export ANY_DATA_DIR="$HOME/.any-sandbox"
export ANY_INDEX_EMBEDDER=none
any init
any run
```

An authorized server can now serve local data calls. Without `any init` in this root, it would start unauthorized and data calls would return `401 auth.required`. Two devices on the same LAN can still find each other: set `p2p.enabled: false` in the chosen config to disable that discovery. The example selects `none` to avoid an embedding-model download; node configuration alone is not a guarantee of zero network traffic.

## Push pairs with the network

The push-notification node is a direct out-of-band peer, *not* part of the nodeconf. The packaged production push node is applied only when the network is also the production default; a staging or local server (and every test) gets no push node unless `push.peerId` / `push.addrs` name one explicitly, so nothing on a test network ever pushes through production ([Push](../notifications/push.html)).

## Embedded builds

`any.aar` and `any.xcframework` follow the same rule with one parameter: an empty `nodeconfYAML` in `Start` selects the embedded production conf (so a mobile host vendors no conf of its own and changes network with a binding bump); non-empty YAML text overrides it ([Android](android.html), [iOS](ios.html)).

## Checking which network you are on

```bash
any sync-status space $SPACE        # networkPeers > 0 once a tree node is connected
any debug space $SPACE              # per-peer head-sync counters (diagnostic, unstable)
```

With no reachable network or LAN peer, sync status remains offline and `networkPeers` is zero. A server with reachable peers can move to `syncing` and `synced`; inspect the status rather than assuming a fixed startup time.

> **Why it matters.** Because every device holds the whole database, "which network" only decides *who relays your ciphertext and to whom*. The data model, the API, and the encryption are identical on production, staging, a LAN, or no network at all — which is what lets a test suite run the real server against a conf that goes nowhere.

See also: [Networks (operations)](../operations/networks.html) for running your own nodes, and [Configuration](../operations/configuration.html) for the full `network` block.
