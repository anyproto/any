---
title: anyrt
description: Run the anybao agent runtime next to your any server — write anybao.toml, seed an LLM key, start anyrt serve, and schedule your first program with a trigger record.
order: 80
---
# anyrt

Run a program or agent beside the Any server. `anyrt` executes Python in a WebAssembly sandbox; `anyrt serve` watches chat and runs scheduled triggers. This page uses a separate runtime process. A desktop host can embed the runtime in-process.

Complete the [HTTP quickstart](curl.html) first. Any remains the local data server. An agent's language-model provider is a separate choice from the server's search embedder; selecting local embeddings does not make agent model calls local.

## Prerequisites

- An `any` server running on `127.0.0.1:7001` with an account ([Install](install.html)).
- The anybao repo, built:

```bash
git clone https://github.com/anyproto/anybao && cd anybao
nix develop                 # canonical environment (or: direnv allow)
uv sync
make kernel                 # componentized CPython → bin/kernel.wasm (required before cargo)
make runtime                # → runtime/target/release/anyrt
export PATH="$PWD/runtime/target/release:$PATH"
```

The kernel is compiled into the binary. Keep the shell in this checkout for the file examples below, and keep `runtime/target/release` on PATH when opening another terminal. Use a runtime revision compatible with your Any server; their preview contracts evolve together.

## 1. anybao.toml

Host-side config lives in one TOML file in the working directory. CLI flags override file values; secrets never appear in it.

```toml
addr = "http://127.0.0.1:7001"        # the any server

[agent]
space = "bao"                         # working space: chat, memory, triggers, your programs
name = "bao"
control_port = 7010

[overlays]                            # program repos: values are SPACE IDS, never names.
                                      # invite ⇒ join on boot (read-only)
agent = { space = "bafyreig…", invite = "2gChBt…" }

[paths]
traces = "traces"                     # local trace folder: anyrt run traces, trace blobs
```

`agent.space = "bao"` names the account's derived agent space — every device of the account resolves the same space id, so there is nothing to create by hand. The `agent` overlay is the repo space that ships the agent's own programs and skills; your working space can shadow any module by name ([Modules and overlays](../programs/modules-and-overlays.html)).

## 2. Seed the LLM key

Environment variables are not read. Put the key in a dotenv-style `.connectors.env` next to `anybao.toml` (gitignored), keyed by the secret's *ref*:

```
llm.key.anthropic=sk-ant-…
```

On every `serve` start each ref is written through to the agent's secrets store in the working space and the file's value wins; an empty value deletes the stored secret. The store syncs across your own devices, end-to-end encrypted like every change, so a key entered on one device reaches the one running the agent. `--secrets-file <path>` adds a file whose refs win over `.connectors.env` ([Credentials](../programs/credentials.html)).

## 3. Start the agent

Programs and skills load from spaces, not the filesystem. With the `agent` overlay configured, the agent's own programs come from that repo space and there is nothing to publish first:

```bash
anyrt serve                                          # watch the chat, run conversations + triggers
```

```
anyrt serving space=bafyreig… chat=bafyreic… control=127.0.0.1:7010
```

Send a message to that chat — the working space's general chat, in the web UI at `http://127.0.0.1:7001/ui` or with `any chat send <space> <chat> --text …` — and the agent answers. Without an `agent` overlay the alias binds to the working space itself, which then needs one `anyrt deploy --source repos/_agent` (hash-gated: unchanged files are skipped) before the first serve. Redeploying while it runs takes effect on the next conversation — no restart.

## 4. Run a program by hand

`anyrt run` executes one program's `main(args)` from a local folder and writes a trace — the offline dev loop:

```python
# myrepo/programs/hello@v1.py
def main(args):
    print("hello", args.get("name", "world"))       # print() is the traced output channel
    return {"ok": True}
```

```bash
anyrt run hello@v1 --programs myrepo/programs --args '{"name":"you"}'
anyrt trace ls --program hello                       # newest first
anyrt trace show <run_id> --stats                    # per-turn tokens, cells, effects
```

Publish it to the working space so a trigger can run it. `deploy` without `--target` writes to the working space, creating it if needed:

```bash
anyrt deploy --source myrepo
```

## 5. Your first trigger

A schedule is a record in the `agent_triggers` dataset on the working space's trigger anchor object. The anchor is the `bao/triggers/v1` child of the `bao/v1` bundle, typed with the harness's hidden `agent_trigger` type — resolve both, never search by name. The child route requires the `type`, and the dataset's storage collection is `<typeId>_agent_triggers`; read the name from the space's dataset listing:

```bash
API=http://127.0.0.1:7001/v1
BAO=$(any space derived | jq -er '.spaces[] | select(.name=="bao") | .spaceId')
TRG=$(curl -s "$API/spaces/$BAO/types?includeHidden=true" \
  | jq -er '.types[] | select(.xKey=="agent_trigger") | .id')
ANCHOR=$(curl -s -X POST $API/spaces/$BAO/bundles/bao%2Fv1/children \
  -H 'content-type: application/json' \
  -d '{"seed":"bao/triggers/v1","type":"'$TRG'"}' | jq -er .objectId)
TRIGGERS=$(curl -s $API/spaces/$BAO/datasets \
  | jq -r '.datasets[].name | select(endswith("_agent_triggers"))')
```

Upsert the trigger record — `kind`, `spec`, `program`, `args`, `enabled`:

```bash
curl -s -X POST $API/spaces/$BAO/modify -H 'content-type: application/json' -d '{
  "objectId": "'$ANCHOR'", "dataset": "'$TRIGGERS'",
  "records": [{ "id": "hello-every-10m", "upsert": true, "ops": [
    { "type": "$set", "path": "", "value": {
        "name": "hello every 10 minutes",
        "kind": "cron", "spec": { "every_s": 600 },
        "program": "hello@v1", "args": { "name": "cron" },
        "enabled": true } } ] }] }'
```

Within one 5-second tick the running `serve` on the account's active device adopts the record, stamps its peer id into `owner`, and schedules the first fire `every_s` ahead. A `once` trigger is `{"kind":"once","spec":{"at": <epoch seconds>}}`; a cron expression is `{"cron": "0 8 * * *"}`, evaluated in UTC. The record carries only scheduler state:

```bash
curl -s -X POST $API/spaces/$BAO/query -H 'content-type: application/json' \
  -d '{"objectId":"'$ANCHOR'","dataset":"'$TRIGGERS'","limit":10}' \
  | jq '.records[] | {id, owner, lastStatus, lastRunAt, consecutiveFailures}'
```

Three consecutive failures auto-disable the trigger (`enabled: false`, `lastStatus: "auto_disabled"`); a trigger that can never fire is marked `invalid_spec` rather than staying silent. Each fire's history — status, duration, cost, and the `runId` that `anyrt trace show <runId> --addr http://127.0.0.1:7001` renders — is a synced summary in `agent_runs`, keyed by `triggerId` ([Runs and monitoring](../scheduling/runs-and-monitoring.html)).

## Embedded instead of standalone

Apps run the same agent in-process as a Rust library — no agent binary, no asset tree:

```rust
let mut cfg = anyrt::Config::builder()
    .addr("http://127.0.0.1:7001")
    .agent_space("bao")
    .overlay_with_invite("agent", "<repoSpaceId>", "<inviteToken>")
    .build();
anyrt::config::bootstrap(&mut cfg);
let agent = anyrt::serve::start(cfg)?;     // returns once booted; the loops run on their own threads
// … agent.stop() on shutdown
```

> **Note.** Agents on different devices of the account elect one active instance, so only one of them answers the chat. Two agents against the same `any` server share its device identity and both answer — run one agent per server.

Next: [Programs and effects](../understanding/programs-and-effects.html) for the model, [Writing a program](../programs/writing-a-program.html) for the details.
