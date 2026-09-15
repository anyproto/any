# CLI

## Principles

- **Default mode is client.** `any space get <id>` sends a GET to the
  running server.
- **Running the server is an explicit subcommand.** `any run`.
- **Commands map 1:1 onto endpoints.** Not every endpoint has a
  command; the ones below without one are HTTP-only (`03-api.md`).
- **Output is JSON.** Pretty-printed (easier for both humans and piping
  through `jq`); streams print one JSON object per frame. No table
  rendering.
- **Exit codes**: 0 success, 1 user / 4xx error, 2 server / 5xx error,
  3 can't reach server.

## Command surface

### Meta

```
any init [--mnemonic "w1 … w12"] [--mnemonic-stdin] [--index N] [--new]
         [--data-dir DIR] [--config PATH] [--wallet PATH] [--passkey-stdin]
                                 # create data dir + account wallet, exit
any run [--config PATH] [--data-dir DIR] [--mode standalone|managed] [--account ID]
        [--wallet PATH] [--log-level LEVEL]
                                 # start the server (foreground; --addr is the bind address)
any auth login [--mnemonic ...|--mnemonic-stdin|--account ID] [--index N] [--replace]  # POST /v1/auth
any auth logout                  # DELETE /v1/auth (managed servers)
any auth status                  # GET /v1/auth
any status                       # GET /v1/health
any stop [--data-dir DIR] [--config PATH] [--account ID]
                                 # signal the server serving the data dir's account
any version                      # print the binary version, and the server's when one answers
```

`any init` is the explicit first-run flow. Bare `init` generates a
fresh account under `<root>/<accountId>/`, prints the BIP-39 mnemonic
to stderr once and `{accountId, created}` to stdout; when any account
already exists it is a no-op that lists them. `--mnemonic` /
`--mnemonic-stdin` authorize an EXISTING account: the same phrase
always derives the same account id while the device key is freshly
generated — the supported way to add a second device (never copy
`wallet.key`: that clones the device key and the two peers fight over
one network identity). Prefer `--mnemonic-stdin`; a `--mnemonic` flag
value leaks into shell history. `--new` forces an additional fresh
account; `--index` selects the derivation index for `--mnemonic` and
defaults to 1 — the `any` account index (0 is anytype's, so one phrase
serves both products with distinct accounts). Restoring an account
derived at index 0 needs an explicit `--index 0`.

`any run` does not create wallets (outside an explicit `--wallet` path).
With no account resolvable (fresh root, or several accounts and no
`--account`/`ANY_ACCOUNT` selector) the server starts unauthorized and
waits; `any auth login` (or any client POSTing `/v1/auth`) generates
(no flags), restores (`--mnemonic*`) or selects (`--account`) the
account and boots the SDK in place. `any auth status` shows the
authorization state, the ownership mode with its capability bits, and
every account found in the data dir.

`--mode managed` starts a host-owned server (`02-server.md` § Modes):
it never resolves an account from disk, keeps no wallet (the phrase
arrives over `POST /v1/auth` on every boot, the device key is cached
per account), and prints its control token as the second stdout line
(`CONTROL_TOKEN <hex>`). Driving one from the CLI needs that token —
`--control-token` / `ANY_CONTROL_TOKEN` — on `auth login`, `auth
logout` and `auth login --replace` (switch to another account in
place). A standalone server refuses `auth logout` and `--replace`.

Prefer `ANY_CONTROL_TOKEN` over `--control-token`: a flag value is
visible in the process list, and it never appears in `--help` output
either way.

`any stop` sends no HTTP: it finds the server serving the data dir's
account by its held instance lock, sends it `SIGTERM`, waits for the
lock to be released and prints `{stopped, account, pid}` — so it works
against a wedged server, one on an ephemeral port, and a managed one
alike. With several accounts running under one root pick one with
`--account` (`--account default` names the legacy flat-root account);
an unauthorized standalone server holds no account lock — stop it with
Ctrl-C. `--addr` is refused on `stop` (it resolves by data dir, not
address). Not available on Windows, which has no signal to send: stop
the server with Ctrl-C in its terminal. `POST /v1/shutdown` is the
managed host's path, refused on a standalone server.

### Account

```
any account                                         # GET /v1/account
any account set-metadata --name "..." [--description "..."] [--icon-cid CID]
any account redeem <code>                           # POST /v1/account/access-code
```

### Identities (account-global directory)

```
any identities list                                 # GET /v1/identities
any identities get <identity>                       # GET /v1/identities/:identity
any identities subscribe                            # SSE: added/updated/removed
```

The account-global, device-local directory of every identity this account
has encountered (across spaces, 1-1s, inbox invites) — profiles plus the
spaces where each was seen. Use it to resolve a display name/icon for an
account id you hold (a chat author, a 1-1 peer). Aliased `any contacts`.
It carries **no rights** — for roles (owner/admin/writer/reader) read
`any members list <spaceId>`. A contact's `name` is empty until their
profile decryption key arrives (shared space / 1-1) and resolves.

### Devices (device registry & active-app election)

```
any devices list                                    # GET /v1/devices — rows + active map + self
any devices register [--name N] [--app slug[=ver]]... [--remove-app slug]...
                                                    # PUT /v1/devices/me (self-row only)
any devices activate <app>                          # POST /v1/devices/activate — claim on THIS device
any devices remove <peerId> --yes                   # DELETE /v1/devices/:peerId (permanent for that peer id)
any devices query [--filter ...] [--sort ...] [--limit N] [--offset N] [--total] [--projection ...]
                                                    # POST /v1/devices/query — raw rows
any devices subscribe [same flags]                  # POST /v1/devices/query/subscribe (SSE)
```

The account's device registry (tech-space `devices` dataset): per-app
install flags and the active-instance election. `os`/`version` are
server-stamped on boot; `register` only sets the caller-owned fields.
`remove` is guarded by `--yes` because tombstones are sticky — a pruned
peer id can never re-register. Election contract and decision matrix:
[devices](23-devices.md).

### Spaces

```
any space get    <spaceId>
any space update <spaceId> [--name ...] [--description ...] [--icon-cid CID]   # PATCH
any space settings <spaceId> [--set k=v]... [--set-bool k=true|false]... [--set-num k=N]... [--unset k]...
any space delete <spaceId> --yes                    # delete a space (irreversible)
any space sync   <spaceId>                          # force a head-sync round now
any space derived                                   # list well-known derived spaces (name, spaceId, created)
any space derived create <name>                     # materialize one (idempotent)
any space query      [--filter JSON] [--sort ...] [--limit N] [--offset N] [--total] [--projection ...] [--dataset spaces|profile]
any space subscribe  [same flags]                   # windowed space-list SSE
any datasets [<spaceId>]                            # dataset schemas (JSON Schema + x-scope)
any search <spaceId> <query> [--scopes basic,chat,props] [--limit N] [--mode hybrid|fts|vector] [--require T ...] [--exclude T ...] [--max-data N] [--passages N] [--filter JSON|@FILE|-]
any backlinks <spaceId> <objectId> [--record ID --dataset NAME | --prop ID] [--kind K ...] [--limit N]
any backlinks --target <any://…> [--kind K ...] [--limit N]         # across every indexed space
any links <spaceId> <objectId> [--record ID --dataset NAME | --prop ID] [--kind K ...] [--limit N]
```

`any backlinks` / `any links` wrap `GET …/objects/:id/backlinks`,
`GET …/objects/:id/links` and `GET /v1/backlinks` — the link index the
search indexer maintains (`docs/13-index.md` § Links; wire shape in
`docs/03-api.md` § Links and backlinks). `--record` + `--dataset` or
`--prop` narrow to one part of the object; `--kind` (repeatable) keeps
only those edge kinds (`mention`, `link`, `card`, `embed`, `relation`);
`--limit` defaults to and caps at 500.

`any search` wraps `POST /v1/spaces/:spaceId/search` — the server's
local FTS + vector index over chats, editor blocks, and properties
(contract in `docs/13-index.md`). Default mode is `hybrid`; without an
embedder configured on the server it degrades to FTS (the reply's
`mode` says which ran). The `query` accepts `"quoted phrases"` and
`prefix*` on the lexical leg; `--require` / `--exclude` (repeatable) add
must / must-not terms; `--max-data N` bounds each hit's `data` window
(default 512 runes, `-1` = the whole chunk). `--limit N` counts
records (one hit per record, default 10, max 100); `--passages N`
(max 10) adds a record's next best matching chunks to its hit;
`--filter` (inline JSON, `@FILE`, or `-` for stdin) keeps only hits
whose host object matches an `/objects/query` filter — e.g.
`'{"any.types":{"$nin":["bin"]}}'` to search outside the bin, or a
type id to search within an app's objects — in every mode
(`docs/03-api.md` § search).

`any space query` / `any space subscribe` wrap `POST /v1/spaces/query`
and `/query/subscribe` (`Service.Query` over the tech-space `spaces`
dataset): a filterable / sortable snapshot and a live SSE stream of the
account's space list. `any space subscribe` prints one JSON frame per
line on stdout, same shape as `any query-subscribe`. Records are the raw
tech-index rows; `GET /v1/spaces` (the mapped `SpaceInfo` list) has no
command.

`any datasets` dumps dataset schemas as JSON Schema with a per-field
`x-scope` (synced / derived / local). With a `<spaceId>` it lists every
dataset the space hosts (`Space.Datasets`); without one it lists the
account's tech-space system datasets (`Service.Datasets`).

`any space sync` wraps `Space.SyncHeads`: it forces an immediate
head-sync (diff) round and blocks until it completes (printing nothing
on success). Use it to converge on demand instead of waiting for the
periodic headsync timer.

`any space derived` is the registry of well-known derived spaces,
resolved and materialized by name (`docs/03-api.md` § Spaces → Derived
spaces).

`any space update` uses cobra's `Changed` semantics: a flag left unset
leaves the field as-is, a flag set to an empty string clears it.

`any space settings` wraps `PATCH /v1/spaces/:id/settings`
(`Spaces().SetSettings`) — the **account-private** per-space settings
object on the tech-space row, distinct from `any space update` (which
writes the member-replicated name/description/icon). Values are
scalars: `--set` takes the value as a bare string; `--set-bool` /
`--set-num` are the typed variants (explicit flags, no literal
sniffing — the string `"true"` stays writable). All four flags repeat;
keys are single-level (no dots). Push reads `notifyMode`
(`all | mentions | none`) from here — e.g.
`any space settings $SPID --set notifyMode=mentions`. Read the object
back off `any space get` (`settings` field) or the raw
`any space query` rows. See `docs/20-push.md` § Settings.

`any space delete` wraps `DELETE /v1/spaces/:id` (`Service.Delete`). It
is irreversible, so it refuses to run without `--yes`. Deletion is
offline-first: the server writes the synced `deleted` tombstone and
reclaims local storage (immediately in the normal case; a partial sweep
failure keeps the storage file so the next boot retries), then drives
the signed coordinator delete in the background. The row stays in the
space list with `status:"deleted"` (sticky tombstone), so a subsequent
`any space query` still shows it.

Creating a space and listing `SpaceInfo` rows have no command
(`POST /v1/spaces`, `GET /v1/spaces`).

### One-to-one (direct) spaces

```
any one-to-one start    <otherIdentity>                       # open/accept a 1-1 by peer identity
any one-to-one accept   <spaceId>                             # accept an incoming pending 1-1
any one-to-one decline  <spaceId>                             # decline an incoming pending 1-1 (sticky)
any one-to-one register <peerIdentity> [--name ...] [--description ...] [--icon-cid CID]   # register an out-of-band incoming request
any one-to-one pending                                        # list incoming pending requests
```

A 1-1 (direct) space is shared by exactly two identities, derived from
both account keys (same id regardless of who initiates). The peer's
account identity is the `id` from their `any account` output, exchanged
out-of-band. `start` reaches out (active immediately); the other side
either discovers it automatically (coordinator inbox → a
`one_to_one_pending` row, listed by `any one-to-one pending`) or has the
app `register` it out-of-band, then `accept` / `decline` it. `decline` is
synced + sticky account-wide; a later `start <peer>` un-declines.
Endpoints + state machine: `docs/03-api.md` § Spaces (and the SDK's
[`docs/13-one-to-one-spaces.md`](https://github.com/anyproto/any-sync-sdk/blob/main/docs/13-one-to-one-spaces.md)). Aliases: `any 1-1`, `any direct`.

### Aggregate

```
any aggregate <spaceId> <objectId> --dataset <name> --pipeline JSON|@FILE|-
any aggregate <spaceId> --properties --pipeline JSON|@FILE|-
```

Runs a MongoDB-style aggregation pipeline (snapshot, no subscribe
variant) — `POST …/aggregate` over a per-object dataset, or
`POST …/objects/aggregate` over the per-space objects collection with
`--properties`. The pipeline is a JSON array of stages. Optional:
`--group-limit` / `--accum-limit` / `--memory-limit` (blocking-stage
bounds; negative = unlimited) and `--explain` (print the access plan
instead of results). Stage set, examples, and MongoDB divergences in
`14-aggregation.md`.

```bash
any aggregate $SPID $OBJID --dataset chat_messages \
  --pipeline '[{"$group":{"_id":"$creator","n":{"$count":{}}}},{"$sort":{"n":-1}}]'
```

### Editor

```
any editor blocks create <spaceId> <objectId> --type T [--text "..."] [--style JSON] [--parent ID] [--pos LEXID] [--collection NAME]
any editor blocks patch  <spaceId> <objectId> <blockId> [--set JSON] [--unset PATH]... [--collection NAME]
any editor blocks delete <spaceId> <objectId> <blockId> [--collection NAME]
any editor edit          <spaceId> <objectId> --old TEXT --new TEXT [--all] [--collection NAME]
any editor edit          <spaceId> <objectId> --edits JSON|@FILE|- [--collection NAME]
```

`editor blocks` maps 1:1 onto the atomic block write endpoints
(reads go through `any query-subscribe … --dataset <collection>`). Every
editor command takes `--collection` (default `editor_blocks`, the
canonical collection a shared editor part declares); pass a namespaced
`<typeId>_<key>` to address a part's own editor. The object must carry
a type whose part declares the collection (`dataset.not_declared`
otherwise — see `03-api.md` § Parts and modules).

`editor edit` is `PATCH …/editor/:collection/markdown` — targeted oldText →
newText replacements against the rendered markdown. Each `oldText`
must match the current rendering exactly (whole-line fuzzy fallback
tolerates unicode punctuation and trailing whitespace) and, unless
`--all` / `"replaceAll"`, occur exactly once. `--edits` takes a JSON
array of `{"oldText","newText","replaceAll"?}` for a multi-spot batch;
all edits match against the original document and any failure rejects
the whole request. See `docs/03-api.md` § Objects.

```bash
any editor edit $SPID $OBJID --old '- [ ] buy milk' --new '- [x] buy milk'
```

### Chat

```
any chat send   <spaceId> <objectId> --text "..." | --file FILE|-  [--reply-to <msgId>]
                [--agent-name NAME [--agent-debug-link LINK] [--agent-done=false]]
any chat edit   <spaceId> <objectId> <msgId> --text "..." | --file FILE|-
any chat delete <spaceId> <objectId> <msgId>
any chat react  <spaceId> <objectId> <msgId> <emoji>
```

The `<objectId>` for a space's chat is the `rootId` of the catalog's
`system:general-chat/v1` bundle — `any catalog setup general-chat
<spaceId>` (§ Catalog below) installs or adopts it and prints the root.
The chat module is reserved to the server, so that is the one chat a
space has.

`text` is markdown; `--file -` reads from stdin so multi-line content
pipes in cleanly (`cat msg.md | any chat send … --file -`). Edit and
delete only work on your own messages (server returns 403 otherwise).
React is a toggle — adds the emoji on the first call, removes on the
second. `--agent-name` marks the message agent-authored under that
display name; `--agent-debug-link` and `--agent-done` (default true)
require it (`03-api.md` § Chat).

Read and tail messages with the windowed subscribe (§ Subscribe):

```
any query-subscribe <spaceId> <objectId> --dataset chat_messages --sort=-_ver.id --limit 50
```

### Files

```
any file attach   <spaceId> <objectId> <path>|-  [--name N] [--mime M]
                                                 [--variant V --variant-of FILE]
any file list     <spaceId> [--object OBJ] [--limit N]
any file get      <spaceId> <fileId>
any file download <spaceId> <fileId> [-o PATH] [--variant V]
any file status   <spaceId> <fileId>
any file stats    <spaceId>
any file subscribe <spaceId>
any file pin      <spaceId> <fileId>
any file retry    <spaceId> <fileId>
any file offload  <spaceId> <fileId>
any file delete   <spaceId> <fileId> --yes
any file query    <spaceId> <objectId> [--filter J] [--sort K] [--limit N] [--offset N] [--total] [--projection ...]
any file query-subscribe <spaceId> <objectId> [same flags]
any file cache size | free <bytes> | sweep
```

`attach` streams `<path>` (or stdin with `-`) as the raw upload body;
name defaults to the basename and the mime is resolved by the server
(content for binary formats, the stored name's extension for text —
docs/03-api.md § Files; `--mime` overrides). The
printed `FileInfo` receipt normally shows `durable: false` — backup is
background work; watch `any file subscribe` for the `inflight →
durable` flip. `download` writes raw bytes to stdout by default (pipe
them) or to `-o PATH` with a small JSON receipt — the two deliberate
non-JSON outputs in the CLI. `offload` exits non-zero with
`file.not_durable` while the local bytes are the only copy. `delete`
removes the file for every member (variants cascade with their
original) and refuses to run without `--yes`. `query` /
`query-subscribe` read the cleartext payload rows (docs/17-files.md
§ Reads). See `docs/17-files.md` for the model.

### Subscribe

```
any query-subscribe <spaceId> <objectId> --dataset <name> [--filter JSON] [--sort K] [--limit N] [--offset N] [--total] [--projection ...]
any query-subscribe <spaceId> --properties [same flags]
```

Opens the windowed query/subscribe stream — `POST …/query/subscribe`
over a per-object dataset, or `POST …/objects/query/subscribe` over the
per-space objects collection with `--properties` — and prints one JSON
object per SSE frame on stdout, `{"event": "<name>", "data": <payload>}`:
`ready`, `snapshot`, `changes`, `closed`. The first `snapshot` is the
query's answer; the stream stays open for live changes. `--sort` is a
comma-separated key list (`-` prefix = descending) and needs `--limit`;
`--total` adds the unbounded match count and `hasNext`.

```bash
any query-subscribe $SPID --properties \
  | jq 'select(.event=="changes") | .data[]'
```

`--timeout` does not apply (streams are long-lived). Cancel with
Ctrl-C. See `04-events.md` for the contract.

#### `--projection`

Every windowed query / subscribe command (`any query-subscribe`,
`any space query|subscribe`, `any devices query|subscribe`,
`any file query|query-subscribe`, `any local query`) takes
`--projection`: a comma-separated list of field paths to return, `-`
prefixing an exclusion the way `--sort` prefixes a descending key.

```bash
any query-subscribe $SPID --properties --projection 'any,<typeId>'   # only those subtrees
any query-subscribe $SPID --properties --projection '-_ver'          # everything but the version map
```

`id` always comes back and `_ver` narrows to match the fields you
asked for — never name a `_ver` path. Grammar and the divergences from
mongo: `09-query.md` § Projection.

### Types & properties

```
any type create <spaceId> --name "..." --xkey ... [--description "..."] [--icon-cid ...]
                [--weight N] [--layout '<json>'] [--hidden] [--meta k=v ...]
any type update <spaceId> <typeId> [--name ...] [--description ...] [--icon ...]
                [--weight N] [--layout '<json>'|''] [--hidden[=false]] [--meta k=v|k= ...]
any type list   <spaceId> [--include-hidden]

any type property list   <spaceId> <typeId>
any type property add    <spaceId> <typeId> --name ... --kind string|number|boolean|array|object|datetime
                         [--xkey ...] [--description ...] [--scope synced|account|local]
                         [--x-format '<json>|@FILE|-']
any type property patch  <spaceId> <typeId> <propId> --set '<json>' [--unset <path> ...]
any type property remove <spaceId> <typeId> <propId>

# option convenience (sugar over `property patch`):
any type property option set    <spaceId> <typeId> <propId> <key> [--name ...] [--color ...] [--pos ...]
any type property option delete <spaceId> <typeId> <propId> <key>

# parts — display units owning datasets served by a module
# (03-api.md § Parts and modules):
any type part list   <spaceId> <typeId>
any type part add    <spaceId> <typeId> --draft '<json>|@FILE|-'
any type part patch  <spaceId> <typeId> <partId> --set '<json>' [--unset <path> ...]
any type part remove <spaceId> <typeId> <partId>

# the datasets under a part (03-api.md § Runtime dataset schemas);
# `type part list` shows every dataset with its part, `dataset list` flat:
any type part dataset list   <spaceId> <typeId>
any type part dataset add    <spaceId> <typeId> <partId> --draft '<json>|@FILE|-'
any type part dataset patch  <spaceId> <typeId> <defId> --set '<json>' [--unset <path> ...]
any type part dataset remove <spaceId> <typeId> <defId>
any type part dataset field add    <spaceId> <typeId> <defId> --field '<json>|@FILE|-'
any type part dataset field patch  <spaceId> <typeId> <defId> <fieldId> --set '<json>' [--unset <path> ...]
any type part dataset field remove <spaceId> <typeId> <defId> <fieldId>

# batch ingest into an id:user dataset (the record id is the
# idempotency key — identical re-runs are no-ops; NAME is the
# collection, <typeId>_<key> for a records dataset):
any upsert <spaceId> <objectId> --dataset NAME --records '<json>|@FILE|-'
           [--page-size N] [--trace-id ...]
```

Aliases: `any type property` = `prop`, `any type part dataset` = `ds`,
`remove` = `delete` / `rm`.

`type update` patches the display and rendering slice; cobra's
`Changed` distinguishes an absent flag (keep) from an empty one
(clear), and `--layout ''` clears the layout. `--meta k=v` is
repeatable and per key (a value that parses as a JSON scalar is taken
as such, `k=` unsets); `--hidden=false` unhides. `type list` omits
hidden types (a records-hosting bundle root, a type marked hidden)
without `--include-hidden`. A part draft is the
`PartDraftRequest` shape — `{"key": "body", "datasets": [{"module":
"editor", "shared": true}]}` declares a shared editor body; `{"key":
"transcript", "ui": {"type": "table"}, "datasets": [{"key":
"segments", "idRule": "user", "fields": […]}]}` a records dataset in
the namespaced collection `<typeId>_segments`.

`--kind` is pinned; everything descriptive — slug, icon, order, options,
relation targets, per-format config — is the `--x-format` descriptor
(`27-descriptors.md`). `property patch` is the generic `{set, unset}`
write covering rename, the handle and every descriptor path (see
`03-api.md` § Types). `--set` is a JSON map of dotted path → value; a set
targets a leaf, never an object; `--unset` is a repeatable dotted path.
Examples:

```
any type property add S T --name Stage --xkey stage --kind array \
  --x-format '{"type":"choice","options":{"lead":{"name":"Lead","color":"grey","pos":"a0"}}}'
any type property patch S T P --set '{"name":"Priority"}'
any type property patch S T P --set '{"xFormat.options.high.name":"High","xFormat.options.high.color":"red","xFormat.options.high.pos":"a0"}'
any type property patch S T P --set '{"xFormat.config.multiple":true}'
any type property patch S T P --unset xFormat.options.high
any type property option set S T P high --name High --color red --pos a0
any type part dataset field patch S T D F --set '{"description":"Headline","xFormat.icon":"title"}'
```

Property values on objects, type attach / detach (moving an object to
the bin included) and object create / delete have no command
(`03-api.md` § Objects, § Properties (values on objects)).

### Bundles

```
any bundle ensure  <spaceId> --body '<json>|@FILE|-'
any bundle list    <spaceId>
any bundle get     <spaceId> <bundleId>
any bundle resolve <spaceId> <bundleId> <loserRootId>
any bundle child   <spaceId> <bundleId> --seed SEED [--type T ...]
```

`ensure` takes the `BundleEnsureRequest` body (`03-api.md` § Bundles)
— the id, the root strategy (`derived`) and what the root declares:
`parts`, `properties` (each property with an `xKey`; the property id
derives from it) or an `xKey` (the type's handle; alone it declares a
marker type), plus `layout`, `weight`, `hidden` describing that type.
Adopt-or-install: the reply carries the converged row and
whether THIS call installed it. `get` and `list` are locked on
registry convergence and report `synced`. `resolve` deletes a losing
root after its content was merged; `child` derives a setup object
under the winner. Bundle ids are passed verbatim (`favorites/v1`);
the CLI encodes the path. Ids under `system:` are the server's and are
refused.

### Catalog

```
any catalog list
any catalog get   <usecaseId>
any catalog setup <usecaseId> <spaceId>
```

The server's embedded usecase catalog (`03-api.md` § Catalog,
`28-well-known-bundles.md`): `list` and `get` print the entries as
declared; `setup` sets a usecase up in a space, its `requires` first —
adopt-or-install per bundle, idempotent, resumable after a failure —
and prints every bundle touched with the converged row, whether THIS
call installed it, `typeId` and the xKey → propId map. Usecase ids are
slugs (`wiki`, `general-chat`, `crm`), passed as-is. `make
catalog-validate [FILES=…]` checks the catalog file itself — a build
step in the repo, not a CLI command.

### Members, invites & ACL

```
any members list      <spaceId>
any members me        <spaceId>
any members get       <spaceId> <identity>
any members requests  <spaceId>
any members subscribe <spaceId>          # SSE: added/changed/removed

any invite create     <spaceId>
any invite list       <spaceId>
any invite get        <spaceId> <recordId>   # carries inviteToken on the minting account's devices
any invite revoke     <spaceId> <recordId>
any invite revoke-all <spaceId>
any invite guest-key        <spaceId>    # mint/return the public read-only token (owner)
any invite guest-key-revoke <spaceId>    # rotate the read key; old guest tokens die
any invite pending                       # direct-add invites awaiting approval
any invite accept     <spaceId>          # accept a direct-add invite (loads the space)
any invite decline    <spaceId>          # decline (sticky; accept later overrides)

any join --token <invite> [--name N] [--description D] [--icon-cid CID]

any acl accept       <spaceId> --record <requestRecordId> [--permission none|reader|guest|writer|admin]
any acl decline      <spaceId> --identity <identity>
any acl grant        <spaceId> <identity> <permission>
any acl remove       <spaceId> <identity>...
any acl add          <spaceId> <identity>[,<identity>...] <permission> [--name N] [--description D]
any acl ownership    <spaceId> --new-owner <identity> [--old-owner-perm P]
any acl self-remove  <spaceId>
any acl cancel-join  <spaceId>
any acl stop-sharing <spaceId>
```

`any join` takes the token `any invite create` printed; `--name` /
`--description` / `--icon-cid` ride the join record. `acl accept`
grants `writer` unless `--permission` says otherwise; `acl ownership`
leaves the old owner `admin` unless `--old-owner-perm` says otherwise.

`any acl add` adds accounts **by identity** — the whole comma-separated
batch lands in one ACL record, and each added account is notified through
the coordinator inbox (durable, retried): on their side the space shows
up as an `invite_pending` row (`any invite pending`), which they resolve
with `any invite accept` / `any invite decline`. Decline is synced +
sticky account-wide but non-terminal — a later accept overrides it; the
declined account stays on the ACL.

### Sync status

```
any sync-status space     <spaceId>                  # rolled-up state
any sync-status object    <spaceId> <objectId>       # per-object state
any sync-status subscribe                            # account-wide SSE stream
any sync-status subscribe <spaceId> <objectId>       # per-object SSE stream
```

There is no `peers` command: `GET …/sync-status/peers` returns 501.
`any debug space <spaceId>` reports per-peer headsync counters.

`subscribe` emits one JSON object per SSE frame on stdout — the same
wrapper as `any query-subscribe`:

```
{"event": "ready",   "data": {}}
{"event": "status",  "data": {"spaceId":"…","state":"syncing", … }}
{"event": "lagged",  "data": {"total": 3}}
{"event": "closed",  "data": {"reason": "server_shutdown"}}
```

### Events

```
any events publish --type TYPE [--scope device|account|space] [--space ID]
                   [--target X] [--data JSON|@FILE|-]    # POST /v1/events
any events subscribe [--scope S]... [--type T]... [--space ID]... [--target X]...
                                                          # GET /v1/events/subscribe (SSE)
```

The account-wide ephemeral event bus (full contract:
`docs/21-events.md`). `--scope` defaults to `device`; `account` reaches
every device of the account, `space` (with `--space`) every member of
the space, both over the SDK pub/sub. `publish` prints the
`{subscribers}` reply (local matches; 0 = nobody listening, still
success). `subscribe` filter flags are repeatable — AND across
dimensions, OR within one; `--type` takes an exact type or a `x.*`
prefix — and it emits one JSON object per SSE frame on stdout, same
wrapper as `any query-subscribe`:

```
{"event": "ready",  "data": {}}
{"event": "event",  "data": {"type":"ui.open_space","scope":"device","data":{…},"sender":{…}}}
{"event": "closed", "data": {"reason": "server_shutdown"}}
```

UI navigation example:

```
any events publish --type ui.open_space  --data '{"spaceId":"SPACE"}'
any events publish --type ui.open_object --data '{"spaceId":"SPACE","objectId":"OBJ"}'
any events subscribe --type 'ui.*'
```

### Processes

```
any process list                        # GET  /v1/processes
any process cancel ID [--identity X]    # POST /v1/processes/:id/cancel
```

The live process view over the event bus (full contract:
`docs/22-processes.md`) — last-event-wins, in-memory, staleness-swept.
`cancel` emits `process.cancel` toward the owner, who reacts and
emits the terminal event; when several publishers run the same id it
exits 1 with `409 process.ambiguous` — pass `--identity` to pick one.
Registration/progress/finish are owner API calls (agents use the HTTP
endpoints); watch the raw frames with
`any events subscribe --type 'process.*'`.

### Local store

```
any local meta                                        # GET    /v1/local/meta
any local collections [--scope account|space] [--space ID]   # GET /v1/local/collections
any local ensure NAME [--space ID] [--index a,-b]... [--unique-index k]...   # PUT /v1/local/collections
any local drop NAME [--space ID] --yes                # DELETE /v1/local/collections
any local insert NAME [--space ID] --doc JSON         # POST   /v1/local/insert   (object or array; @FILE / -)
any local upsert NAME [--space ID] --doc JSON         # POST   /v1/local/upsert
any local update NAME ID [--space ID] --modifier JSON [--upsert]        # POST /v1/local/update
any local delete NAME [ID...] [--space ID] [--filter JSON] --yes        # POST /v1/local/delete
any local get NAME ID [--space ID]                                      # POST /v1/local/get
any local query NAME [--space ID] [--filter JSON] [--sort a,-b] [--limit N] [--offset N] [--total] [--projection ...]   # POST /v1/local/query
any local aggregate NAME [--space ID] --pipeline JSON [--group-limit N] [--accum-limit N] [--memory-limit N] [--explain]
any local indexes NAME [--space ID] [--ensure a,-b]... [--unique-ensure k]... [--drop NAME]...       # POST /v1/local/indexes
```

Device-local, never-synced collections (`docs/26-local-store.md`).
NAME is always the first argument; `--space ID` binds the collection
to a space, otherwise it is account-scoped. `drop` and `delete` refuse
without `--yes` — local data has no backup. Pipelines name sink /
lookup collections by their `storageName` (shown by `collections`).

### Push notifications

```
any push token set --platform ios|android --token TOKEN   # POST /v1/push/token
any push token revoke                                      # DELETE /v1/push/token
any push token status                                      # GET /v1/push/token (local state)
any push subscriptions                                     # GET /v1/push/subscriptions
```

Device-token registration and the account's server-held push topic
subscriptions (full contract: `docs/20-push.md`). All four need a push
node on the server (`push.peerId` / `push.addrs`, or the production
default — `05-config.md`) — otherwise `409 push.disabled` (exit 1).
`token set` / `token revoke` print nothing on success; `token status`
reports the LOCAL persisted state (no push-node round trip);
`subscriptions` rows are raw `{spaceKey, topic}` pairs — `spaceKey` is
the base58 space push public key, not a spaceId. Notify preferences are
set via `any space settings <spaceId> --set notifyMode=…` (per-space
default) and the `chat.notifyMode` property on a chat object (per-chat
override).

### Debug (diagnostic)

```
any debug space  <spaceId>                  # per-peer headsync counters (in-memory)
any debug object <spaceId> <objectId>       # tree + sync snapshot (one-shot; walks the tree)
any debug p2p                               # GET /v1/debug/p2p — LAN listener, discovery, known peers
```

Diagnostic surface; **not stable** — fields move with the SDK's
`DebugAPI`. Production callers read `any sync-status`. `any debug
object` locks the object tree and walks every change, so don't poll it
in a tight loop.

## Global flags

```
--addr <host:port>     # where the server is; default: the address the ONE
                       # running server under the data dir recorded
                       # (<account-dir>/server.addr; --account / ANY_ACCOUNT
                       # picks among several), else 127.0.0.1:7001. An
                       # unauthorized server holds no account lock and is not
                       # discovered — pass --addr to reach it on a non-default port
--control-token <hex>  # managed server's control token (or ANY_CONTROL_TOKEN);
                       # sent as X-Any-Control-Token on every request
--timeout <duration>   # request timeout; default 30s
--verbose              # log HTTP request/response to stderr
```

Pretty-printed JSON is the only output format.

## Input formats

- JSON-valued flags (`--body`, `--draft`, `--field`, `--x-format`,
  `--records`, `--pipeline`, `--edits`, `--data`, `--doc`, `--modifier`,
  local `--filter`) take inline JSON, `@FILE`, or `-` for stdin.
- `--filter` on the windowed query commands, `--set` on the patch
  commands, `--style` and `--layout` take inline JSON.
- Scalar flags (`--name`, `--kind`, etc.) set simple fields on the body.
- `--sort` and `--projection` are comma-separated lists; a `-` prefix
  means descending / exclude.

## When the server isn't running

Exit 3 with:

```
any: cannot reach server at 127.0.0.1:7001
  start it with `any run` in another terminal
```

No auto-start.
