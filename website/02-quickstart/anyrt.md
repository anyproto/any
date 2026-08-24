---
title: anyrt
description: Run the anybao agent runtime next to your any server — write anybao.toml, seed an LLM key, start anyrt serve, and schedule your first program with a trigger record.
order: 80
---
# anyrt

anyrt is the runtime that executes Python programs inside a wasm cage against your any server, and `anyrt serve` is the agent built on it: it watches a chat, answers, and runs scheduled triggers. This page gets one running standalone; the desktop app embeds the same runtime in-process.

## Prerequisites

- An `any` server running on `127.0.0.1:7001` with an account ([Install](install.html)).
- The anybao repo, built:

```bash
git clone https://github.com/anyproto/anybao && cd anybao
nix develop                 # canonical environment (or: direnv allow)
uv sync
make kernel                 # componentized CPython → bin/kernel.wasm (required before cargo)
make runtime                # → runtime/target/release/anyrt
```

The kernel is compiled into the binary; `anyrt` is one artifact.

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
traces = "traces"                     # device-local run traces
```

`agent.space = "bao"` names the account's derived agent space — every device of the account resolves the same space id, so there is nothing to create by hand. The `agent` overlay is the repo space that ships the agent's own programs and skills; your working space can shadow any module by name ([Modules and overlays](../programs/modules-and-overlays.html)).

## 2. Seed the LLM key

Environment variables are not read. Put the key in a dotenv-style `.connectors.env` next to `anybao.toml` (gitignored), keyed by the secret's *ref*:

```
llm.key.anthropic=sk-ant-…
```

On every `serve` start each ref is written through to a device-local, never-synced store in the space and the file's value wins; an empty value deletes the stored secret. `--secrets-file <path>` names a different file ([Credentials](../programs/credentials.html)).

## 3. Publish programs and start the agent

Programs and skills load from spaces, not the filesystem, so a fresh working space needs one deploy before the first serve:

```bash
anyrt deploy --source repos/_agent --target bao      # hash-gated: unchanged files are skipped
anyrt serve                                          # watch the chat, run conversations + triggers
```

Send a message in the space's general chat (web UI at `http://127.0.0.1:7001/ui`, or `any chat send`) and the agent answers. Redeploying while it runs takes effect on the next conversation — no restart.

## 4. Run a program by hand

`anyrt run` executes one program's `main(args)` from a local folder and writes a trace — the offline dev loop:

```python
# repos/_agent/programs/hello@v1.py
def main(args):
    print("hello", args.get("name", "world"))       # print() is the traced output channel
    return {"ok": True}
```

```bash
anyrt run hello@v1 --args '{"name":"you"}'
anyrt trace ls --program hello                       # newest first
anyrt trace show <run_id> --stats                    # turns, cells, effects
```

## 5. Your first trigger

A schedule is a record in the `agent_triggers` dataset on the working space's trigger anchor object. The anchor is the `bao/triggers/v1` child of the `bao/v1` bundle — resolve it, never search by name:

```bash
API=http://127.0.0.1:7001/v1
BAO=$(any space derived | jq -r '.[] | select(.name=="bao") | .spaceId')
ANCHOR=$(curl -s -X POST $API/spaces/$BAO/bundles/bao%2Fv1/children \
  -H 'content-type: application/json' -d '{"seed":"bao/triggers/v1"}' | jq -r .objectId)
```

Upsert the trigger record — `kind`, `spec`, `program`, `args`, `enabled`:

```bash
curl -s -X POST $API/spaces/$BAO/modify -H 'content-type: application/json' -d '{
  "objectId": "'$ANCHOR'", "dataset": "agent_triggers",
  "records": [{ "id": "hello-every-10m", "upsert": true, "ops": [
    { "type": "$set", "path": "", "value": {
        "name": "hello every 10 minutes",
        "kind": "cron", "spec": { "every_s": 600 },
        "program": "hello@v1", "args": { "name": "cron" },
        "enabled": true } } ] }] }'
```

The running `serve` adopts the record within a tick, stamps its device's peer id into `owner`, and fires it. A `once` trigger is `{"kind":"once","spec":{"at": <epoch seconds>}}`; a cron expression is `{"cron": "0 8 * * *"}`. "Did it run?" is on the record itself:

```bash
curl -s -X POST $API/spaces/$BAO/query -H 'content-type: application/json' \
  -d '{"objectId":"'$ANCHOR'","dataset":"agent_triggers","limit":10}' \
  | jq '.records[] | {id, lastStatus, lastRunAt, runCount, consecutiveFailures, lastRunRef}'
```

`lastRunRef` is a trace ref; `anyrt trace show` renders it. Three consecutive failures auto-disable the trigger (`enabled: false`, `lastStatus: "auto_disabled"`); a trigger that can never fire is marked `invalid_spec` rather than staying silent ([Scheduling](../scheduling/index.html)).

## Embedded instead of standalone

Apps run the same agent in-process as a Rust library — no agent binary, no asset tree:

```rust
let mut cfg = anyrt::Config::builder()
    .addr("http://127.0.0.1:7001")
    .agent_space("bao")
    .overlay_with_invite("agent", "<repoSpaceId>", "<inviteToken>")
    .build();
anyrt::config::bootstrap(&mut cfg);
let agent = anyrt::serve::start(cfg)?;     // non-blocking
// … agent.stop() on shutdown
```

> **Note.** Run one agent per working space. A standalone `anyrt serve` and a desktop app embedding anyrt against the same space are two agents on one chat — every message gets two replies.

Next: [Programs and effects](../understanding/programs-and-effects.html) for the model, [Writing a program](../programs/writing-a-program.html) for the details.
