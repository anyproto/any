---
title: Modules and overlays
description: How use() resolves a program from a space, why import can't do it, and how repos are published and joined as overlays.
order: 30
---
# Modules and overlays

Programs load other programs with `use("name@vN")`. Resolution is host-mediated, version-exact and recorded, so the trace pins the exact bytes that ran — even after the program in the space has moved on. A **repo** is a folder of programs published to a space; other spaces join it read-only and address it by an **overlay alias** — `use("agent:llm@v1")` names a program in the repo aliased `agent`.

## `use()` versus `import`

| | `import x` | `use("x@vN")` |
|---|---|---|
| source | bundled into the kernel | an object in a space |
| version | fixed at kernel build | resolved per call, cached by marker |
| determinism | by construction | recorded (`module.resolve` effect) |
| dependency resolution | global | bound to the owning space |

`import` is the kernel's frozen world — the stdlib allowlist, same bytes on every machine, nothing for the trace to record. `use()` loads code whose answer can change between runs, machines and deploys; under the isolation principle that is an effect. There is deliberately no import-hook sugar: an innocent-looking `import` must never perform a failable, space-dependent effect.

```python
ws = use("webSearch@v1")            # the working space
ws = use("agent:webSearch@v1")      # overlay alias from anybao.toml, strict
ws = use("<spaceId>:webSearch@v1")  # explicit space, strict
```

## Resolution order

1. `name@vN` → the working space. **Never an overlay.** A copy in your own space deliberately shadows shipped code.
2. `alias:name@vN` → that overlay's space, strict. Aliases come from `[overlays]` in `anybao.toml`.
3. `spaceId:name@vN` → strict.
4. **Transitive**: an unqualified `use()` inside a loaded module resolves in the module's *defining* space. Every loaded module gets a `use` bound to its owner, so an overlay program is self-contained and never silently pulls dependencies from the consumer's space.

Cross-repo dependencies are therefore alias-qualified in the source (`use("agent:llm@v1")`). An unqualified miss answers with a hint naming the configured aliases.

## What a load records

```jsonc
{"effect": "module.resolve", "cell": "toolu_01a",
 "input":  {"spec": "webSearch@v1", "frm": null},
 "output": {"spaceId": "…", "objectId": "…",
            "marker": 4711,
            "sourceHash": "sha256:…",
            "source": "\"\"\"Grounded web search …",
            "cache": "miss"},
 "meta":   {"class": "read", "mocked": false, "durMs": 12}}
```

The output carries the source itself (spilled to a blob reference when it exceeds the 64 KB threshold), not only its hash: strict replay returns recorded outputs instead of fetching, so a trace replays bit-exact after the program was edited or deleted. `frm` names the requesting module — `null` for cell code — so the whole import tree reconstructs from the log.

The marker is the change counter of the program's source record. Every `use()` resolves the spec again; the kernel keeps loaded modules keyed `(objectId, marker)`, so an unchanged program reuses its module and an edit is live on the very next `use()`.

## Repos and `anybao.toml`

```toml
addr = "http://127.0.0.1:7001"   # the any server

[agent]
space = "bao"              # working space: chat, memory, your own programs
name = "bao"
control_port = 7010

[overlays]                 # alias → space id (never a name)
agent = { space = "bafy…", invite = "<guest token>" }   # invite ⇒ join on boot
std = "bafy…"

[paths]
traces = "traces"

[config]                   # agent config seeds, flat dotted keys
"llm.tier.codegen" = { provider = "anthropic", model = "…" }
```

Host precedence: built-in defaults < `anybao.toml` < CLI flag. Unknown keys are a parse error. The `[config]` table is written through to the working space's agent config at serve start, where `config.get` reads it. Secrets never appear here — see [Credentials](credentials.html).

A repo folder is self-describing: `<src>/programs/`, `<src>/skills/`, and a `README.md` whose content becomes the overlay's description. The `agent` overlay is special only in that the agent loop injects its programs and skills into the initial context; it is deployed like any other. With no `agent` entry the alias binds the working space itself, so a config-less setup behaves like a single-space install.

## Publishing

```sh
anyrt deploy --source repos/_agent --target agent          # alias from [overlays]
anyrt deploy --source repos/_agent --target <spaceId> --addr http://127.0.0.1:7002
```

Deploy is hash-gated and upsert-only: unchanged programs are skipped, and it ensures the `program` type in the target space, then writes each program's source record plus the derived `name` / `version` / `any_tool` / `summary` properties after validating the docstring budget and tool shape. `--target` is strict — it never creates a space. A running agent picks the change up on its next `use()`; no restart.

## Joining an overlay

An overlay published by another account must be joined before it syncs to this device. With an `invite` in the config, the runtime sends `POST /v1/spaces/join {inviteToken}` on boot and proceeds; only program resolution waits for the sync. The token is normally the space's **guest key** — `any invite guest-key <spaceId>` — which grants read-only membership with no approval step, so a repo's authenticity is enforced by the CRDT ACL: only the publisher can write.

```sh
# on the publishing account's server
REPO=$(curl -s -X POST http://127.0.0.1:7002/v1/spaces -d '{"name":"my-repo"}' | jq -r .id)
anyrt deploy --addr http://127.0.0.1:7002 --source repos/_agent --target $REPO
TOKEN=$(any --addr 127.0.0.1:7002 invite guest-key $REPO | jq -r .inviteToken)
```

> **Why it matters.** An overlay is a package registry with no registry service. Publisher identity is the space's ACL, distribution is sync, and a consumer's device holds the full source of everything it can run — offline, encrypted, and pinned per run by content hash.

## Two run surfaces, never mixed

| Surface | Resolves from |
|---|---|
| `anyrt serve` and its control API's `POST /run` | spaces only — working space + overlay aliases; it structurally cannot read program source from disk |
| `anyrt run <spec>` | the `--programs` directory only — default in the [anyrt CLI reference](../reference/anyrt-cli.html) (flat file wins over folder on a tie) |
| `anyrt run --from-space <space>` | serve's resolver, one-shot — tests the *deployed* source |

A broker has either the local directory or the space resolver, so every `module.resolve` record in one trace answers from one world.
