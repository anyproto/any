# One-time conversion of prototype bookmarks

Normal catalog setup creates the Bookmark type for new spaces. It does not
convert the collection-shaped prototype. Existing prototype accounts receive
`catalog.definition_conflict` until their definition is converted explicitly.

`scripts/convert-legacy-bookmarks.py` is a standalone maintenance tool for these
few early accounts. It is not included in server, desktop or extension artifacts.
Run it on macOS or Linux with Python 3's standard library and the running server's
loopback HTTP API;
it neither starts a server nor creates/logs into an account. The server must
advertise `conditional-writes` in `/v1/health`.

## Run

Sync the early devices first and update their clippers. Pause Bookmark content and schema editing on all devices during conversion.
In particular, the existing add-part endpoint has no revision guard; adding the
missing editor body assumes no concurrent schema edit. Keep an ordinary account backup too: the tool's
private JSON file contains the definition and affected object rows, not a full
account backup or editor-dataset export. The tool never rewrites editor records.
Use the existing server's address and the target space ID; never provide a
recovery phrase or account key to this tool.

Preview (no writes):

```sh
python3 scripts/convert-legacy-bookmarks.py --addr "$ANY_ADDRESS" --space-id "$SPACE_ID"
```

Apply after reviewing the count, with a new backup filename:

```sh
python3 scripts/convert-legacy-bookmarks.py --addr "$ANY_ADDRESS" --space-id "$SPACE_ID" \
  --apply --backup before-bookmark-conversion.json
```

The backup is created exclusively with mode 0600 and flushed before any write.
Keep it private: it contains saved URLs, notes and other object values. An existing
backup file is never overwritten. Output reports counts, not captured content.
After success, retry Bookmark setup or the queued clipper save normally.

## Preservation and interruption

The definition becomes a type using the same ID and property IDs. Each legacy
page keeps its ID, URL, notes, clipped text, custom fields, editor body and links.
Only its type and the old Bookmark collection membership change; other collection
memberships remain. The tool declares the canonical editor body if missing.
It refuses members with an unrelated custom type and incompatible editor parts.

Definition promotion and member conversion carry the revision of the read snapshot. A newer applied edit causes a
conflict, never a stale overwrite. These guards protect against locally applied
changes; they are not a distributed transaction against unseen offline writes.

On conflict, rejection, timeout or lost response, the tool stops without retrying
that request. Inspect the error and saved rows, rerun the preview, then rerun with
`--apply` and a **new** backup filename. Remaining collection membership identifies
unfinished work; already converted members are not rewritten. No deletion or
rollback is automatic. Resolve custom-type conflicts individually; do not blindly
restore a backup over newer edits.

An old offline client can later create another legacy member. Normal setup never
sweeps those in. Sync and update those clients, then explicitly run the tool again
if needed. New users need no conversion: their setup remains install extension →
Connect.

## Verification

`go test ./internal/server` runs the tool against a real isolated offline SDK
account and executes its Python unit suite. Coverage includes 103 members,
dry-run, private backup, exact value/body preservation, repeated conversion,
interruption/resumption and concurrent root/type/membership changes. The standalone
unit suite also runs with `python3 scripts/test_convert_legacy_bookmarks.py`.
