---
title: Reference
description: Exhaustive catalogs of the `any` server and `anyrt` runtime surfaces — endpoints, commands, streams, error codes, config keys, effects and schemas.
order: 0
---
# Reference

The guides explain how to build with `any`; this section lists what exists. Every endpoint, command, frame, error code and config key is here once, in the shape you will see on the wire, so you can look something up without reading a chapter.

## How the pieces fit

```
your client / script / agent
        │  HTTP + JSON, SSE           (http-api, events, errors)
        ▼
any server  127.0.0.1:7001            (config, cli)
        │  encrypted CRDT sync
        ▼
sync network + your other devices

anyrt serve  127.0.0.1:7010           (anyrt-cli, anybao-toml)
   └─ sandboxed programs ── effects ──▶ any server, LLM, web   (effects-catalog)
   └─ triggers                                                 (trigger-schema)
```

Two binaries, two config files, one API. The `any` CLI is a thin client over the HTTP API — every subcommand is one endpoint — and `anyrt` is a client of the same API from inside a sandbox.

## Conventions used throughout

- Paths are relative to `http://127.0.0.1:7001`; `…` in a path stands for the `/v1/spaces/:spaceId` prefix already shown in that table.
- `$SP`, `$OBJ`, `$CHAT` in examples are a space id, an object id and a chat object id you substitute.
- Dates on the wire are instants: `{"$date": "2026-08-05T17:00:00.000Z"}` — a bare string is a different value type and silently matches nothing in a filter.
- Property values are keyed by `propId`, never by `xKey`; resolve the handle before you write.
- Dataset writes return `{versionId, changeId, recordIds}`, not the record — read it back through a query or subscription.
- Every non-2xx response is `{"error": {"code", "message", "details?"}}`; batch routes can also report per-item `rejections` inside a 2xx.

<div class="cards">
<a href="http-api.html"><strong>HTTP API</strong><span>Every endpoint, grouped, with bodies, returns and error codes</span></a>
<a href="cli.html"><strong>CLI</strong><span>Every any command group with flags and exit codes</span></a>
<a href="events.html"><strong>SSE streams</strong><span>Every live stream, its frames, and the shared close reasons</span></a>
<a href="errors.html"><strong>Errors</strong><span>The envelope, status rules, and the full code namespace</span></a>
<a href="config.html"><strong>Server configuration</strong><span>Every key, env var, flag, default and precedence</span></a>
<a href="anyrt-cli.html"><strong>anyrt CLI</strong><span>run, serve, deploy, trace, drift — and the control API</span></a>
<a href="effects-catalog.html"><strong>Effects catalog</strong><span>The complete sandbox syscall surface and its capabilities</span></a>
<a href="trigger-schema.html"><strong>Trigger schema</strong><span>agent_trigger fields, kinds, runs, and control routes</span></a>
<a href="anybao-toml.html"><strong>anybao.toml</strong><span>Every key of the runtime's host config</span></a>
<a href="glossary.html"><strong>Glossary</strong><span>One-line definitions with a link to the page that covers each</span></a>
</div>
