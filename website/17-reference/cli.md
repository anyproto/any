---
title: CLI
description: Every any command group with its flags — a thin HTTP client over the local server, one command per endpoint, JSON on stdout.
order: 20
---
# CLI

`any` is one binary in two roles: `any run` starts the server, and every other subcommand is an HTTP call to it. The CLI never opens the SDK or touches storage, so anything you can do from the terminal you can do from `curl` — and vice versa.

## Global flags and exit codes

```
--addr <host:port>     # bind address for `run`; connect address otherwise — default: the address
                       # the running server recorded (<account-dir>/server.addr), else 127.0.0.1:7001
--control-token <hex>  # managed server's control token; prefer ANY_CONTROL_TOKEN (a flag is visible in ps)
--timeout <duration>   # request timeout, default 30s (does not apply to streams)
--verbose              # log the HTTP request/response to stderr
```

| Exit code | Meaning |
|---|---|
| `0` | success |
| `1` | user error — bad arguments, or a 4xx from the server |
| `2` | server error — a 5xx |
| `3` | transport error — the server cannot be reached |

Output is pretty-printed JSON, always. The two deliberate exceptions are `any file download` (raw bytes to stdout) and streaming commands, which print one JSON object per SSE frame — `{"event": "<name>", "data": <payload>}` — so they pipe cleanly into `jq`. When the server is not running the CLI exits 3 and prints `start it with any run in another terminal`; there is no auto-start.

Input conventions: JSON-valued flags (`--pipeline`, `--records`, `--draft`, `--body`, `--x-format`, …) accept `'<json>'`, `@FILE` or `-` for stdin; `--filter` is a Mongo-style filter object; windowed queries take `--projection 'any,<typeId>'` or `'-_ver'`.

## Meta and auth

```
any init [--mnemonic "w1 … w12"] [--mnemonic-stdin] [--index N] [--new]
         [--config PATH] [--data-dir PATH] [--account ID] [--wallet PATH] [--passkey-stdin]
any run  [--config PATH] [--data-dir PATH] [--mode standalone|managed] [--account ID]
         [--addr host:port] [--wallet PATH] [--passkey-stdin]
         [--log-level debug|info|warn|error]
any auth login [--mnemonic …|--mnemonic-stdin|--account ID] [--index N] [--replace]   # POST /v1/auth
any auth logout                                                   # DELETE /v1/auth (managed)
any auth status                                                   # GET /v1/auth
any status                                                        # GET /v1/health
any stop [--config PATH] [--data-dir PATH] [--account ID]         # signal the server holding the account lock
any version                                                       # binary version, plus the server's when one runs
```

`--mode managed` starts a host-owned server: it never resolves an account from disk, the phrase arrives over `POST /v1/auth` on every launch, and it prints its control token as the second stdout line (`CONTROL_TOKEN <hex>`). `auth login --replace` (switch in place) and `auth logout` are managed-only and need that token (`ANY_CONTROL_TOKEN`). `any stop` sends no HTTP and takes no `--addr`: it finds the server by its held account lock and signals it — so it works against a wedged server and one on an ephemeral port; a standalone server refuses `POST /v1/shutdown`. Not available on Windows (no signal to send — stop the server with Ctrl-C).

`any init` creates the data dir and an account wallet, printing the BIP-39 mnemonic to stderr once. With `--mnemonic` / `--mnemonic-stdin` it restores an existing account (same phrase, same account id, a fresh device key) — prefer stdin so the phrase stays out of shell history. `--index` defaults to 1 (the any derivation index; 0 restores an anytype-derived account); `--new` forces an additional account. `any run` never creates wallets: with no resolvable account it starts unauthorized and waits for `any auth login`. See [Accounts](../auth/accounts.html).

## Account, identities, devices

```
any account set-metadata [--name N] [--description D] [--icon-cid CID]   # PUT /v1/account/metadata
any account redeem <code>                                               # POST /v1/account/access-code

any identities list | get <identity> | subscribe     # alias: any contacts

any devices list                                     # rows + active map + self
any devices register [--name N] [--app slug[=ver]]... [--remove-app slug]...
any devices activate <app>
any devices remove <peerId> --yes                    # permanent for that peer id
any devices query     [--filter J] [--sort K] [--limit N] [--offset N] [--total] [--projection P]
any devices subscribe [same flags]
```

`GET /v1/account` has no subcommand — call it with `curl`.

## Spaces

```
any space get      <spaceId>
any space update   <spaceId> [--name N] [--description D] [--icon-cid CID]   # unset flag = keep, --name='' = clear
any space settings <spaceId> [--set k=v]... [--set-bool k=true|false]... [--set-num k=N]... [--unset k]...
any space delete   <spaceId> --yes
any space sync     <spaceId>                         # force one head-sync round
any space derived                                    # list well-known derived spaces
any space derived create <name>                      # materialize one (idempotent)
any space query      [--dataset spaces|profile] [--filter J] [--sort K] [--limit N] [--offset N] [--total] [--projection P]
any space subscribe  [same flags]                    # live space list, one frame per line
any datasets [<spaceId>]                             # dataset schemas (JSON Schema + x-scope)
any search <spaceId> <query> [--scopes basic,chat,props] [--limit N]
           [--mode hybrid|fts|vector] [--require T]... [--exclude T]...
           [--max-data N] [--passages N] [--filter J]
```

`any space settings` writes the account-private per-space settings object; push reads `notifyMode` from it: `any space settings $SP --set notifyMode=mentions`. `any search` defaults to `hybrid` and degrades to FTS when the server has no embedder — the reply's `mode` says which ran; `--limit` counts records, `--max-data` bounds each hit's text window (default 512 runes, `-1` = whole chunk), `--passages` adds up to 10 further matching chunks per record, `--filter` keeps hits whose host object matches an objects-query filter (inline JSON, `@FILE` or `-`).

Space create and list have no dedicated subcommand — use `curl` against `POST /v1/spaces` and `GET /v1/spaces`; join with `any join --token T` below.

## One-to-one spaces

```
any one-to-one start    <otherIdentity>              # aliases: any 1-1, any direct
any one-to-one accept   <spaceId>
any one-to-one decline  <spaceId>                    # sticky; a later start un-declines
any one-to-one register <peerIdentity> [--name N] [--description D] [--icon-cid CID]
any one-to-one pending                               # incoming requests
```

## Catalog and bundles

```
any catalog list                                     # GET /v1/catalog
any catalog get   <usecaseId>
any catalog setup <usecaseId> <spaceId>              # adopt-or-install, dependencies first

any bundle ensure  <spaceId> --body '<json>'|@FILE|-
any bundle list    <spaceId>
any bundle get     <spaceId> <bundleId>
any bundle resolve <spaceId> <bundleId> <loserRootId>
any bundle child   <spaceId> <bundleId> --seed SEED --type T [--collection C]...
```

`catalog setup` prints every bundle it touched with its `typeId` or `collectionId` — whichever kind the bundle declares — and its xKey → propId `properties`, so the ids a client writes with come from the reply, never from minting a definition by xKey.

```bash
any catalog setup wiki $SP
```

## Reads and writes

```
any query-subscribe <spaceId> <objectId> --dataset NAME [--filter J] [--sort K] [--limit N] [--offset N] [--total] [--projection P]
any query-subscribe <spaceId> --properties [same flags]      # per-space objects storage collection
any aggregate <spaceId> <objectId> --dataset NAME --pipeline '<json>'|@FILE|-
any aggregate <spaceId> --properties --pipeline '<json>'|@FILE|-
              [--group-limit N] [--accum-limit N] [--memory-limit N] [--explain]
any upsert <spaceId> <objectId> --dataset NAME --records '<json>'|@FILE|- [--page-size N] [--trace-id T]...
any backlinks <spaceId> <objectId> [--record R --dataset D | --prop P] [--kind K]... [--limit N]
any backlinks --target <any://uri> [--kind K]... [--limit N]  # across every indexed space
any links     <spaceId> <objectId> [--record R] [--dataset D] [--prop P] [--kind K]... [--limit N]
```

`query-subscribe` prints the `ready` → `snapshot` → `changes` → `closed` frames as JSON lines; `--limit` is required when `--sort` is set. `--kind` takes `mention`, `link`, `card`, `embed` or `relation`.

```bash
any query-subscribe $SP $CHAT --dataset chat_messages --sort=-_ver.id --limit 50 \
  | jq 'select(.event=="changes") | .data[]'
```

Snapshot query, modify and delete-records have no subcommand — call `POST /v1/spaces/:id/query`, `/modify`, `/delete-records` directly.

## Editor

```
any editor blocks create <spaceId> <objectId> --type T [--text S] [--style JSON] [--parent ID] [--pos LEXID] [--collection NAME]
any editor blocks patch  <spaceId> <objectId> <blockId> [--set JSON] [--unset PATH]... [--collection NAME]
any editor blocks delete <spaceId> <objectId> <blockId> [--collection NAME]
any editor edit          <spaceId> <objectId> --old TEXT --new TEXT [--all] [--collection NAME]
any editor edit          <spaceId> <objectId> --edits '<json>'|@FILE|- [--collection NAME]
```

`--collection` names the editor collection (default `editor_blocks`; a namespaced `<typeId>_<key>` for a part with its own editor). `editor edit` is `PATCH …/editor/:collection/markdown`: each `oldText` must match the current rendering exactly (whole-line fuzzy fallback for unicode punctuation and trailing whitespace) and, without `--all`, exactly once. The example needs `- [ ] buy milk` in the document; once it applies, running it again answers `markdown.no_match`.

```bash
any editor edit $SP $DOC --old '- [ ] buy milk' --new '- [x] buy milk'
```

## Chat

```
any chat send   <spaceId> <objectId> --text S | --file FILE|-  [--reply-to MSG]
                [--agent-name N] [--agent-debug-link L] [--agent-done=false]
any chat edit   <spaceId> <objectId> <msgId> --text S | --file FILE|-
any chat delete <spaceId> <objectId> <msgId>
any chat react  <spaceId> <objectId> <msgId> <emoji>          # toggle
```

`<objectId>` is the `rootId` of the space's chat — `any catalog setup general-chat <spaceId>` installs or adopts it and prints the root. Text is markdown; `--file -` reads it from stdin. Edit and delete work on your own messages only. Reading is `any query-subscribe … --dataset chat_messages`.

## Objects

```
any object create <spaceId> --type T [--collection C]... [--properties '<json>'|@FILE|-]

any object type set          <spaceId> <objectId> <typeId>
any object collection attach <spaceId> <objectId> <collectionId>
any object collection detach <spaceId> <objectId> <collectionId>
```

`--type` is required (`page` is the plain document) and `object type set` replaces the previous one — there is no unset. `--collection` repeats; attach and detach are idempotent, and detaching is not a delete: that owner's values stay on the row as orphan data. `--properties` seeds values keyed owner → propId → value, the owner being the type or one of the collections.

`set` and `attach` pre-flight the id (`404 type.not_found` / `404 collection.not_found`, `400 type.not_a_type` / `400 collection.not_a_collection` when it names the other surface); `detach` pre-flights nothing, so it can repair a row that already carries a bogus id.

```bash
any object create $SP --type page --collection $WIKI
any object create $SP --type $PERSON --collection $CONTACT --properties '{"any":{"name":"Ada"}}'
any object collection attach $SP $OBJ bin     # to the bin
any object collection detach $SP $OBJ bin     # restore
```

## Types and properties

```
any type create <spaceId> --name N --xkey K [--description D] [--icon-cid CID]
                [--layout '<json>'] [--hidden] [--meta k=v]...
any type list   <spaceId> [--include-hidden]
any type update <spaceId> <typeId> [--name N] [--description D] [--icon CID]
                [--layout '<json>'|''] [--hidden[=false]] [--meta k=v]...

any type property list   <spaceId> <typeId>                        # alias: any type prop
any type property add    <spaceId> <typeId> --name N --kind string|number|boolean|array|object|datetime
                         [--xkey K] [--description D] [--scope synced|account|local] [--x-format '<json>'|@FILE|-]
any type property patch  <spaceId> <typeId> <propId> --set '<json>' [--unset PATH]...
any type property remove <spaceId> <typeId> <propId>
any type property option set    <spaceId> <typeId> <propId> <key> [--name N] [--color C] [--pos LEXID]
any type property option delete <spaceId> <typeId> <propId> <key>

any type part list   <spaceId> <typeId>
any type part add    <spaceId> <typeId> --draft '<json>'|@FILE|-
any type part patch  <spaceId> <typeId> <partId> --set '<json>' [--unset PATH]...
any type part remove <spaceId> <typeId> <partId>

any type part dataset list   <spaceId> <typeId>                    # alias: any type part ds
any type part dataset add    <spaceId> <typeId> <partId> --draft '<json>'|@FILE|-
any type part dataset patch  <spaceId> <typeId> <defId> --set '<json>' [--unset PATH]...
any type part dataset remove <spaceId> <typeId> <defId>
any type part dataset field add    <spaceId> <typeId> <defId> --field '<json>'|@FILE|-
any type part dataset field patch  <spaceId> <typeId> <defId> <fieldId> --set '<json>' [--unset PATH]...
any type part dataset field remove <spaceId> <typeId> <defId> <fieldId>
```

```bash
any type property add $SP $T --name Stage --xkey stage --kind array --x-format '{"type":"choice"}'
any type property patch $SP $T $P --set '{"xFormat.options.high.name":"High","xFormat.options.high.color":"red"}'
any type property option set $SP $T $P high --name High --color red --pos a0    # same write, sugar
```

## Collections

```
any collection list   <spaceId> [--include-hidden]
any collection get    <spaceId> <collectionId>
any collection create <spaceId> --name N --xkey K [--description D] [--icon-cid CID]
                      [--hidden] [--meta k=v]...
any collection update <spaceId> <collectionId> [--name N] [--description D] [--icon CID]
                      [--hidden[=false]] [--meta k=v]...

any collection property list   <spaceId> <collectionId>            # alias: any collection prop
any collection property add    <spaceId> <collectionId> --name N --kind string|number|boolean|array|object|datetime
                               [--xkey K] [--description D] [--scope synced|account|local] [--x-format '<json>'|@FILE|-]
any collection property patch  <spaceId> <collectionId> <propId> --set '<json>' [--unset PATH]...
any collection property remove <spaceId> <collectionId> <propId>
```

A collection is what an object is filed under; a type is what it is. A collection takes a name, description, icon, `--xkey`, `--hidden`, `--meta` and property definitions — no parts, no layout. The property verbs are the type ones on a collection owner, same flags and same `{set, unset}` patch rules; the choice-option sugar is on the type group only, so patch the descriptor path directly here.

`--xkey` is required on create and unique across the space's types and collections together (`409 type.xkey_conflict`). `collection list` omits hidden collections (the built-in `miniapp` / `bin`, and any you marked hidden) without `--include-hidden`; `collection get` resolves them always. A registered built-in refuses a metadata write (`400 collection.registered`), and deleting a collection is `501 sdk.not_implemented`.

```bash
any collection create $SP --name Contacts --xkey contact
any collection property add $SP $C --name Company --xkey company --kind string
any collection update $SP $C --hidden=false --meta pinned=true
```

## Files

```
any file attach   <spaceId> <objectId> <path>|-  [--name N] [--mime M] [--variant V --variant-of FILE]
any file list     <spaceId> [--object OBJ] [--limit N]
any file get      <spaceId> <fileId>
any file download <spaceId> <fileId> [-o PATH] [--variant V]     # raw bytes to stdout by default
any file status   <spaceId> <fileId>
any file stats    <spaceId>
any file subscribe <spaceId>
any file pin | retry | offload <spaceId> <fileId>
any file delete   <spaceId> <fileId> --yes
any file query    <spaceId> <objectId> [--filter J] [--sort K] [--limit N] [--offset N] [--total] [--projection P]
any file query-subscribe <spaceId> <objectId> [same flags]
any file cache size | free <bytes> | sweep
```

The attach receipt normally shows `durable: false` — backup is background work; `any file subscribe` shows the `inflight → durable` flip. `offload` exits 1 with `file.not_durable` while the local bytes are the only copy.

## Local store

```
any local meta                                       # pipeline stages and accumulators
any local collections [--scope account|space] [--space ID]
any local ensure  <name> [--space ID] [--index 'a,-b']... [--unique-index 'k']...
any local drop    <name> --yes [--space ID]
any local insert | upsert <name> --doc '<json>'|@FILE|- [--space ID]
any local update  <name> <id> --modifier '<json>' [--upsert] [--space ID]
any local delete  <name> [<id>...] [--filter J] --yes [--space ID]
any local get     <name> <id> [--space ID]
any local query   <name> [--filter J] [--sort K] [--limit N] [--offset N] [--total] [--projection P] [--space ID]
any local aggregate <name> --pipeline '<json>' [--group-limit N] [--accum-limit N] [--memory-limit N] [--explain] [--space ID]
any local indexes <name> [--ensure 'a,-b']... [--unique-ensure 'k']... [--drop NAME]... [--space ID]
any local export  [--scope account|space] [--space ID] [--names a,b] --out FILE
any local import  FILE                               # - reads stdin
```

Without `--space` a local storage collection is account-scoped; with it, bound to that space. `export` with neither scope nor names takes every local collection. Nothing here syncs.

```bash
any local ensure scratch --index k,-at
any local insert scratch --doc '[{"id":"a","k":1},{"k":2}]'
any local query scratch --filter '{"k":{"$gt":0}}' --sort -k --total
```

With that collection on an authorized server, export and re-import it:

```bash
any local export --scope account --names scratch --out scratch.anyenc.gz &&
any local import scratch.anyenc.gz
```

`--out -` writes the export to stdout; `any local import -` reads stdin. Without `--names`, export takes every collection in the selected scope; `--space ID` implies space scope. The file is a gzip-compressed anyenc stream carrying scope, space id, names, indexes and documents. Another authorized server imports it with `any --addr http://127.0.0.1:7002 local import scratch.anyenc.gz`; a space-scoped collection is readable there even when that server never had the space.

Import ensures indexes and upserts documents; documents whose ids are absent from the file stay. It commits in chunks, so a failed import can leave earlier chunks in place. It copies local collections only — no CRDT data, no file bytes. Responses and failure codes: [HTTP contract](http-api.html#export-and-import-local-collections).

## Members, invites, ACL

```
any members list | me | requests <spaceId>
any members get <spaceId> <identity>
any members subscribe <spaceId>

any invite create | list | revoke-all <spaceId>
any invite get | revoke <spaceId> <recordId>
any invite guest-key <spaceId>                      # public read-only token (owner)
any invite guest-key-revoke <spaceId>               # rotate the read key
any invite pending                                  # direct-add invites awaiting approval
any invite accept | decline <spaceId>

any join --token T [--name N] [--description D] [--icon-cid CID]

any acl accept       <spaceId> --record R [--permission P]          # R from `any members requests`; default writer
any acl decline      <spaceId> --identity X
any acl grant        <spaceId> <identity> <permission>
any acl remove       <spaceId> <identity>...
any acl add          <spaceId> <identity>[,<identity>...] <permission> [--name N] [--description D]
any acl ownership    <spaceId> --new-owner X [--old-owner-perm P]   # default admin
any acl self-remove | cancel-join | stop-sharing <spaceId>
```

Permissions are `none`, `reader`, `guest`, `writer`, `admin`, `owner`. `any acl add` adds accounts by identity in one ACL record; each added account sees an `invite_pending` row and resolves it with `any invite accept` / `decline`. `--name` / `--description` apply to a single-identity add only.

## Sync status, events, processes, push, debug

```
any sync-status space     <spaceId>
any sync-status object    <spaceId> <objectId>
any sync-status subscribe [<spaceId> <objectId>]    # account-wide without args

any events publish --type T [--scope device|account|space] [--space ID] [--target X] [--data J|@FILE|-]
any events subscribe [--scope S]... [--type T|x.*]... [--space ID]... [--target X]...

any process list
any process cancel ID [--identity X]

any push token set --platform ios|android --token TOKEN
any push token revoke | status
any push subscriptions

any debug space  <spaceId>                          # per-peer headsync counters
any debug object <spaceId> <objectId>               # tree + sync snapshot (walks the tree)
any debug p2p                                       # LAN discovery snapshot
any local-discovery [on|off]                        # mDNS announce+browse switch (works before auth)
```

```bash
any events publish --type ui.open_space --data '{"spaceId":"'$SP'"}'
any events subscribe --type 'ui.*'
```

> **Note.** `GET …/sync-status/peers` has no subcommand and returns `501` until the SDK exposes a stable per-space peer list; `any debug space` is the diagnostic equivalent. The push commands exit 1 with `push.disabled` unless the server has a push node configured.
