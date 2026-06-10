# CLI

## Principles

- **Default mode is client.** `any space list` sends a GET to the running
  server.
- **Running the server is an explicit subcommand.** `any run`.
- **One command per SDK call.** If the SDK does it, the CLI has a
  subcommand. If it doesn't, the CLI doesn't.
- **Output is JSON.** Pretty-printed (easier for both humans and piping
  through `jq`). No table rendering in v1 — keep it simple.
- **Exit codes**: 0 success, 1 user / 4xx error, 2 server / 5xx error,
  3 can't reach server.

## Command surface

> **v1 status:** Meta, Account, Chat, Editor, Subscribe, Members,
> Invites, Join, ACL, Debug, Sync-status, and `any space {get,update}`
> are wired in `internal/cli/`. Everything else in this doc is the
> planned 1:1 mirror of the HTTP surface — already callable via
> `curl`, but no CLI subcommand yet. Sections that are not yet
> implemented are marked **(planned)** in their headers.

### Meta

```
any init                         # create data dir + wallet, exit
any run [--config PATH]          # start the server (foreground)
any status                       # GET /v1/health
any stop                         # POST /v1/shutdown
any version                      # print binary + server versions
```

`any init` is the explicit first-run flow — prints the generated
mnemonic to stderr and exits. If you skip it and go straight to
`any run`, the server does the same wallet creation on startup and
prints the mnemonic once; `any init` just gives you a moment to copy
it before the server binds anything.

### Account

```
any account                                         # GET /v1/account
any account set-metadata --name "..." [--description "..."] [--icon CID]
```

### Spaces

```
any space get    <spaceId>                          # shipped
any space update <spaceId> [--name ...] [--description ...] [--icon CID]   # shipped (PATCH)
any space sync   <spaceId>                          # shipped — force a head-sync round now
any space query      [--filter JSON] [--sort ...] [--limit N] [--offset N] [--total] [--dataset spaces|profile]   # shipped — windowed space-list snapshot
any space subscribe  [--filter JSON] [--sort ...] [--limit N] [--offset N] [--total] [--dataset spaces|profile]   # shipped — windowed space-list SSE
any datasets [<spaceId>]                            # shipped — dataset schemas (JSON Schema + x-scope)
any search <spaceId> <query> [--scopes basic,chat,agent] [--limit N] [--mode hybrid|fts|vector]   # shipped — local search index
```

`any search` wraps `POST /v1/spaces/:spaceId/search` — the server's
local FTS + vector index over chats, editor blocks, and agent memory
(contract in `docs/11-index.md`). Default mode is `hybrid`; without an
embedder configured on the server it degrades to FTS (the reply's
`mode` says which ran).

`any space query` / `any space subscribe` wrap `POST /v1/spaces/query`
and `/query/subscribe` (`Service.Query` over the tech-space `spaces`
dataset): a filterable / sortable snapshot and a live SSE stream of the
account's space list. `any space subscribe` prints one JSON frame per
line on stdout, same shape as `any query-subscribe`. Records are the raw
tech-index rows — `GET /v1/spaces` (no CLI subcommand yet) stays the
mapped `SpaceInfo` convenience.

`any datasets` dumps dataset schemas as JSON Schema with a per-field
`x-scope` (synced / derived / local). With a `<spaceId>` it lists every
dataset the space hosts (`Space.Datasets`); without one it lists the
account's tech-space system datasets — `spaces` / `profile` —
(`Service.Datasets`).

`any space sync` wraps `Space.SyncHeads`: it forces an immediate
head-sync (diff) round and blocks until it completes (printing nothing
on success). Use it to converge on demand instead of waiting for the
periodic headsync timer — a manual "sync now", or to speed up
multi-peer e2e tests.

Planned (HTTP surface ships; no CLI subcommand yet):

```
any space create --name "..."
any space list
any space delete <spaceId>
any space join <invite>
any space derive [--seed <hex>]
any space one-to-one <otherIdentity>
```

`any space update` uses cobra's `Changed` semantics: a flag left unset
leaves the field as-is, a flag set to an empty string clears it.

### Objects (planned)

```
any object create <spaceId> [--type <typeId>]... [--property <typeId>.<key>=<value>]...
any object derive <spaceId> --seed <hex> [--type <typeId>]...
any object delete <spaceId> <objectId>
```

### Data plane (planned)

```
any query  <spaceId> <objectId> <dataset> [--filter FILE|-] [--sort ...] [--limit N] [--offset N] [--include-variants] [--include-meta]
any modify <spaceId> <objectId> <dataset> --file FILE|-
any delete <spaceId> <objectId> <dataset> <recordId> [<recordId>...]
```

### Chat

```
any chat send   <spaceId> <objectId> --text "..." | --file FILE | -  [--reply-to <msgId>]
any chat list   <spaceId> <objectId> [--before <msgId>] [--after <msgId>] [--limit N]
any chat edit   <spaceId> <objectId> <msgId> --text "..." | --file FILE | -
any chat delete <spaceId> <objectId> <msgId>
any chat react  <spaceId> <objectId> <msgId> <emoji>
```

`text` is markdown; `--file -` reads from stdin so multi-line content
pipes in cleanly (`cat msg.md | any chat send … --file -`). Edit and
delete only work on your own messages (server returns 403 otherwise).
React is a toggle — adds the emoji on the first call, removes on the
second.

For tailing live updates, use the existing subscribe primitive:

```
any subscribe <spaceId> <objectId> --dataset chat_messages
```

The SSE stream carries routing tuples; clients re-`list` for the new
message body when a `changes` frame arrives.

### Subscribe

```
any subscribe <spaceId> <objectId> --dataset <name>
any subscribe <spaceId> --properties
```

Streams CRDT apply events over Server-Sent Events. Output is one JSON
object per SSE frame on stdout — `{"event": "<name>", "data": <payload>}`
— so the stream pipes cleanly through `jq`:

```bash
any subscribe $SPID $OBJID --dataset objects \
  | jq 'select(.event=="changes") | .data[]'
```

`--timeout` does not apply (streams are long-lived). Cancel with
Ctrl-C. See `04-events.md` for the contract.

### Types & properties (planned)

```
any type list <spaceId>
any type create <spaceId> --name "..." [--description "..."]
any type delete <spaceId> <typeId>
any type show <spaceId> <typeId>

any type add-property    <spaceId> <typeId> --name ... --xkey ... --kind string|number|boolean|null|array|object [--items kind]
any type remove-property <spaceId> <typeId> <propId>
any type update-property <spaceId> <typeId> <propId> [--name ...] [--description ...] [--xkey ...] [--xkind ...]

any properties get         <spaceId> <objectId> [--include-variants]
any properties set-base    <spaceId> <objectId> <typeId> --patch FILE|-
any properties set-account <spaceId> <objectId> <typeId> --patch FILE|-
any properties set-device  <spaceId> <objectId> <typeId> --patch FILE|-
any properties attach      <spaceId> <objectId> <typeId>
any properties detach      <spaceId> <objectId> <typeId>
```

### Members, invites & ACL

```
any members list     <spaceId>
any members me       <spaceId>
any members get      <spaceId> <identity>
any members requests <spaceId>

any invite create     <spaceId> [--permissions writer|reader]
any invite list       <spaceId>
any invite revoke     <spaceId> <recordId>
any invite revoke-all <spaceId>

any join <invite>

any acl accept       <spaceId>
any acl decline      <spaceId>
any acl grant        <spaceId> <identity> <permission>
any acl remove       <spaceId> <identity>...
any acl add          <spaceId> <identity> <permission>
any acl ownership    <spaceId>
any acl self-remove  <spaceId>
any acl cancel-join  <spaceId>
any acl stop-sharing <spaceId>
```

### Sync status

```
any sync-status space     <spaceId>                  # rolled-up state
any sync-status object    <spaceId> <objectId>       # per-object state
any sync-status subscribe                            # account-wide SSE stream
any sync-status subscribe <spaceId> <objectId>       # per-object SSE stream
```

`any sync-status peers` is **not** wired — `/sync-status/peers`
returns 501 until the SDK lands a stable per-space peer list. Use
`any debug space <spaceId>` for the diagnostic equivalent today.

`subscribe` emits one JSON object per SSE frame on stdout — the same
wrapper as `any subscribe`:

```
{"event": "ready",   "data": {}}
{"event": "status",  "data": {"spaceId":"…","state":"syncing", … }}
{"event": "lagged",  "data": {"total": 3}}
{"event": "closed",  "data": {"reason": "server_shutdown"}}
```

### Debug (diagnostic)

```
any debug space  <spaceId>                  # per-peer headsync counters (in-memory)
any debug object <spaceId> <objectId>       # tree + sync snapshot (one-shot; walks the tree)
```

Diagnostic surface; **not stable** — fields may move as the SDK's
`DebugAPI` evolves. Production callers should prefer `any sync-status`
once that ships. `any debug object` locks the object tree and walks
every change, so don't poll it in a tight loop.

## Global flags

```
--addr <host:port>     # default 127.0.0.1:7001 — where the server is
--timeout <duration>   # request timeout; default 30s
--verbose              # log HTTP request/response to stderr
```

No `--output` flag in v1 — pretty-printed JSON is the only format.

## Input formats

- `--file FILE` takes a JSON document matching the endpoint's body. `-`
  means stdin.
- `--patch FILE` is the JSON patch object for properties setters
  (keyed by propId or x-key).
- `--filter FILE` is a mongo-style filter object.
- Scalar flags (`--name`, `--kind`, etc.) set simple fields on the body.
- `--property typeId.key=value` — a convenience for `object create`'s
  `InitialProperties`. May appear multiple times. Value is parsed as
  JSON if it starts with `[`, `{`, or a digit; otherwise taken as a
  string. (Good enough for v1; typed coercion later if needed.)

## When the server isn't running

Exit 3 with:

```
any: cannot reach server at 127.0.0.1:7001
  start it with `any run` in another terminal
```

No auto-start.
