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

> **v1 status:** only the **Meta** commands below are wired in
> `internal/cli/`. Everything else in this doc is the planned 1:1
> mirror of the HTTP surface — already callable via `curl`, but no CLI
> subcommand yet. Sections that are not yet implemented are marked
> **(planned)** in their headers.

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

### Account (planned)

```
any account show
any account update-metadata --name "..." [--description "..."] [--icon CID]
```

### Spaces (planned)

```
any space create --name "..."
any space list
any space get <spaceId>
any space delete <spaceId>
any space join <invite>
any space derive [--seed <hex>]
any space one-to-one <otherIdentity>
```

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

### Members & ACL (planned — server returns 501 until SDK lands them)

```
any members list <spaceId>
any members show <spaceId> <identity>
any acl invite         <spaceId> [--permissions writer]
any acl accept         <spaceId> <identity>
any acl decline        <spaceId> <identity>
any acl remove         <spaceId> <identity>
any acl change-perm    <spaceId> <identity> <permission>
any acl transfer-owner <spaceId> <identity>
any acl self-remove    <spaceId>
```

### Sync status (planned — server returns 501 until SDK lands it)

```
any sync-status space  <spaceId>
any sync-status object <spaceId> <objectId>
any sync-status peers  <spaceId>
```

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
