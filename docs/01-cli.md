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

> **v1 status:** Meta, Account, Chat, Editor, Subscribe, Aggregate,
> Members, Invites, Join, ACL, Debug, Sync-status, and
> `any space {get,update}` are wired in `internal/cli/`. Everything else in this doc is the
> planned 1:1 mirror of the HTTP surface — already callable via
> `curl`, but no CLI subcommand yet. Sections that are not yet
> implemented are marked **(planned)** in their headers.

### Meta

```
any init [--mnemonic "w1 … w12"] [--mnemonic-stdin] [--index N] [--new]
                                 # create data dir + account wallet, exit
any run [--config PATH] [--account ID]   # start the server (foreground)
any auth login [--mnemonic ...|--mnemonic-stdin|--account ID]  # POST /v1/auth
any auth status                  # GET /v1/auth
any status                       # GET /v1/health
any stop                         # POST /v1/shutdown
any version                      # print binary + server versions
```

`any init` is the explicit first-run flow. Bare `init` generates a
fresh account under `<root>/<accountId>/` and prints the BIP-39
mnemonic to stderr once; when any account already exists it is a no-op
that lists them. `--mnemonic` / `--mnemonic-stdin` authorize an
EXISTING account: the same phrase always derives the same account id
while the device key is freshly generated — the supported way to add a
second device (never copy `wallet.key`: that clones the device key and
the two peers fight over one network identity). Prefer
`--mnemonic-stdin`; a `--mnemonic` flag value leaks into shell
history. `--new` forces an additional fresh account; `--index` selects
the derivation index for `--mnemonic` and defaults to 1 — the `any`
account index (0 is anytype's, so one phrase serves both products with
distinct accounts). Restoring an anytype-derived account, or an `any`
account created before index 1 became the default, needs an explicit
`--index 0`.

`any run` does NOT create wallets. With no account resolvable (fresh
root, or several accounts and no `--account`/`ANY_ACCOUNT` selector)
the server starts unauthorized and waits; `any auth login` (or any
client POSTing `/v1/auth`) generates (`no flags`), restores
(`--mnemonic*`) or selects (`--account`) the account and boots the SDK
in place. `any auth status` shows the authorization state plus every
account found in the data dir.

### Account

```
any account                                         # GET /v1/account
any account set-metadata --name "..." [--description "..."] [--icon CID]
```

### Identities (account-global directory)

```
any identities list                                 # GET /v1/identities
any identities get <identity>                        # GET /v1/identities/:identity
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
any devices query [--filter ...] [--sort ...]       # POST /v1/devices/query — raw rows
any devices subscribe                               # POST /v1/devices/query/subscribe (SSE)
```

The account's device registry (tech-space `devices` dataset): per-app
install flags and the active-instance election. `os`/`version` are
server-stamped on boot; `register` only sets the caller-owned fields.
`remove` is guarded by `--yes` because tombstones are sticky — a pruned
peer id can never re-register. Election contract and decision matrix:
[devices](21-devices.md).

### Spaces

```
any space get    <spaceId>                          # shipped
any space update <spaceId> [--name ...] [--description ...] [--icon CID]   # shipped (PATCH)
any space settings <spaceId> [--set k=v]... [--set-bool k=true|false]... [--set-num k=N]... [--unset k]...   # shipped — account-private settings PATCH
any space delete <spaceId> --yes                    # shipped — delete a space (irreversible)
any space sync   <spaceId>                          # shipped — force a head-sync round now
any space query      [--filter JSON] [--sort ...] [--limit N] [--offset N] [--total] [--dataset spaces|profile]   # shipped — windowed space-list snapshot
any space subscribe  [--filter JSON] [--sort ...] [--limit N] [--offset N] [--total] [--dataset spaces|profile]   # shipped — windowed space-list SSE
any datasets [<spaceId>]                            # shipped — dataset schemas (JSON Schema + x-scope)
any search <spaceId> <query> [--scopes basic,chat,agent] [--limit N] [--mode hybrid|fts|vector] [--require T ...] [--exclude T ...]   # shipped — local search index
```

`any search` wraps `POST /v1/spaces/:spaceId/search` — the server's
local FTS + vector index over chats, editor blocks, and agent memory
(contract in `docs/13-index.md`). Default mode is `hybrid`; without an
embedder configured on the server it degrades to FTS (the reply's
`mode` says which ran). The `query` accepts `"quoted phrases"` and
`prefix*` on the lexical leg; `--require` / `--exclude` (repeatable) add
must / must-not terms.

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
any space join <invite>
```

(`any space derive` was dropped from the plan — `Service.Derive` is
deliberately not exposed over HTTP; see `docs/03-api.md` § Spaces.)

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
the signed coordinator delete in the background. The row stays in the space list with
`status:"deleted"` (sticky tombstone), so a subsequent `any space query`
still shows it.

### One-to-one (direct) spaces

```
any one-to-one start    <otherIdentity>                       # shipped — open/accept a 1-1 by peer identity
any one-to-one accept   <spaceId>                             # shipped — accept an incoming pending 1-1
any one-to-one decline  <spaceId>                             # shipped — decline an incoming pending 1-1 (sticky)
any one-to-one register <peerIdentity> [--name ...] [--description ...] [--icon-cid CID]   # shipped — register an out-of-band incoming request
any one-to-one pending                                        # shipped — list incoming pending requests
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
`docs/13-one-to-one-spaces.md`). Aliases: `any 1-1`, `any direct`.

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
any editor blocks create <spaceId> <objectId> --type T [--text "..."] [--style JSON] [--parent ID] [--pos LEXID]
any editor blocks patch  <spaceId> <objectId> <blockId> [--set JSON] [--unset PATH]...
any editor blocks delete <spaceId> <objectId> <blockId>
any editor edit          <spaceId> <objectId> --old TEXT --new TEXT [--all]
any editor edit          <spaceId> <objectId> --edits JSON|@FILE|-
```

`editor blocks` maps 1:1 onto the atomic block write endpoints
(reads go through `any query … --dataset editor_blocks`).

`editor edit` is `PATCH …/editor/markdown` — targeted oldText →
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
any chat send   <spaceId> <objectId> --text "..." | --file FILE | -  [--reply-to <msgId>]
any chat list   <spaceId> <objectId> [--before <msgId>] [--after <msgId>] [--limit N]
any chat edit   <spaceId> <objectId> <msgId> --text "..." | --file FILE | -
any chat delete <spaceId> <objectId> <msgId>
any chat react  <spaceId> <objectId> <msgId> <emoji>
```

The `<objectId>` for a space's shared general chat is the
`generalChatObjectId` field of `any space get <spaceId>` — use it
instead of creating a chat object per client. See `docs/03-api.md`
§ Chat → General chat.

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
any file query    <spaceId> <objectId> [--filter J] [--sort K] [--limit N] [--offset N] [--total]
any file query-subscribe <spaceId> <objectId> [same flags]
any file cache size | free <bytes> | sweep
```

`attach` streams `<path>` (or stdin with `-`) as the raw upload body;
name defaults to the basename, mime to the extension's type. The
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

### Types & properties

```
any type create <spaceId> --name "..." --xkey ... [--description "..."] [--icon-cid ...]
any type list   <spaceId>

any type property list   <spaceId> <typeId>
any type property add    <spaceId> <typeId> --name ... [--xkey ...] [--kind ...]
                         [--format-type links|date|datetime|select|multiselect] [--format-ui ...] [--scope ...]
any type property patch  <spaceId> <typeId> <propId> --set '<json>' [--unset <path> ...]
any type property remove <spaceId> <typeId> <propId>

# option convenience (sugar over `property patch`):
any type property option set    <spaceId> <typeId> <propId> <key> [--name ...] [--color ...] [--pos ...]
any type property option delete <spaceId> <typeId> <propId> <key>

# runtime dataset schemas (03-api.md § Runtime dataset schemas):
any type dataset list   <spaceId> <typeId>
any type dataset add    <spaceId> <typeId> --draft '<json>|@FILE|-'
any type dataset patch  <spaceId> <typeId> <defId> --set '<json>' [--unset <path> ...]
any type dataset remove <spaceId> <typeId> <defId>
any type dataset field add    <spaceId> <typeId> <defId> --field '<json>|@FILE|-'
any type dataset field remove <spaceId> <typeId> <defId> <fieldId>

# batch ingest into an id:user dataset (the record id is the
# idempotency key — identical re-runs are no-ops):
any upsert <spaceId> <objectId> --dataset NAME --records '<json>|@FILE|-'
           [--page-size N] [--trace-id ...]
```

`property patch` is the generic `{set, unset}` write covering rename and
select/multiselect option CRUD (see `03-api.md` § Types). `--set` is a
JSON map of dotted path → value; `--unset` is a repeatable dotted path.
Examples:

```
any type property patch S T P --set '{"name":"Priority"}'
any type property patch S T P --set '{"format.options.high.name":"High","format.options.high.color":"red","format.options.high.pos":"a0"}'
any type property patch S T P --unset format.options.high
any type property option set S T P high --name High --color red --pos a0
```

Property **value** read/write stays under `any properties …` (values on
objects), distinct from `any type property …` (the type's definitions):

```
any properties get         <spaceId> <objectId>
any properties set         <spaceId> <objectId> <typeId> --patch FILE|-
any properties attach      <spaceId> <objectId> <typeId>
any properties detach      <spaceId> <objectId> <typeId>
```

### Members, invites & ACL

```
any members list     <spaceId>
any members me       <spaceId>
any members get      <spaceId> <identity>
any members requests <spaceId>

any invite create     <spaceId>
any invite list       <spaceId>          # rows carry inviteToken on the minting account
any invite get        <spaceId> <recordId>
any invite revoke     <spaceId> <recordId>
any invite revoke-all <spaceId>
any invite guest-key        <spaceId>    # mint/return the public read-only token (owner)
any invite guest-key-revoke <spaceId>    # rotate the read key; old guest tokens die
any invite pending                       # direct-add invites awaiting approval
any invite accept     <spaceId>          # accept a direct-add invite (loads the space)
any invite decline    <spaceId>          # decline (sticky; accept later overrides)

any join <invite>

any acl accept       <spaceId>
any acl decline      <spaceId>
any acl grant        <spaceId> <identity> <permission>
any acl remove       <spaceId> <identity>...
any acl add          <spaceId> <identity>[,<identity>...] <permission>
any acl ownership    <spaceId>
any acl self-remove  <spaceId>
any acl cancel-join  <spaceId>
any acl stop-sharing <spaceId>
```

`any acl add` adds accounts **by identity** — the whole comma-separated
batch lands in one ACL record, and each added account is notified through
the coordinator inbox (durable, retried): on their side the space shows
up as an `invite_pending` row (`any invite pending`), which they resolve
with `any invite accept` / `any invite decline`. Decline is synced +
sticky account-wide but non-terminal — a later accept overrides it; the
declined account stays on the ACL (no self-remove in v1).

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

### Push notifications

```
any push token set --platform ios|android --token TOKEN   # POST /v1/push/token
any push token revoke                                      # DELETE /v1/push/token
any push token status                                      # GET /v1/push/token (local state)
any push subscriptions                                     # GET /v1/push/subscriptions
```

Device-token registration and the account's server-held push topic
subscriptions (full contract: `docs/20-push.md`). All four need a push
node configured on the server (`push.peerId` / `push.addrs`) —
otherwise `409 push.disabled` (exit 1). `token set` / `token revoke`
print nothing on success; `token status` reports the LOCAL persisted
state (no push-node round trip); `subscriptions` rows are raw
`{spaceKey, topic}` pairs — `spaceKey` is the base58 space push public
key, not a spaceId. Notify preferences are set via
`any space settings <spaceId> --set notifyMode=…` (per-space default)
and the `chat.notifyMode` property on a chat object (per-chat
override).

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
