---
title: iOS
description: Embed the any server in an iOS app with any.xcframework — a C-archive with five exported functions — then talk HTTP to it on loopback.
order: 70
---
# iOS

On iOS the server is a static library. `any.xcframework` wraps a Go c-archive (device + simulator slices) exporting a handful of C functions; your app starts the server in-process and uses the normal HTTP API from Swift.

## Get the artifact

Every release publishes `any.xcframework.zip`, sha256-pinned in the release notes. Building it requires macOS with Xcode's iOS SDK:

```bash
scripts/build-xcframework.sh dist/ios      # → dist/ios/any.xcframework.zip
```

Unzip and add `any.xcframework` to your target as an embedded binary. The archive runs the index with no embedder, so `/search` runs `fts` only.

## The surface

The header exposes five functions. Strings are C strings:

| Function | Purpose |
|----------|---------|
| `AnyLibStart(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs) -> AnyLibStartResult` | Boot the engine (standalone mode). Blocks until the listener is up. |
| `AnyLibStartWithMode(…, mode, controlToken) -> AnyLibStartResult` | `AnyLibStart` plus the ownership mode: `"managed"` makes the app the owner — the account arrives over `POST /v1/auth` on every launch and sign-out / switch / shutdown need `controlToken` (`X-Any-Control-Token`). Code 5 = bad options. |
| `AnyLibStop()` | Graceful shutdown; waits for the engine to exit. Safe when not running. |
| `AnyLibStopNow()` | Hard stop, returns promptly — for `applicationWillTerminate` or an expiring background task. |
| `AnyLibVersion() -> char *` | The linked archive's version string. Valid for the process lifetime; do **not** free it. |

`AnyLibStart` hands back a struct, by value, with everything you need to react:

```c
typedef struct {
    int32_t code;         // 0 ok
    char    address[64];  // "127.0.0.1:53421" when code == 0
    char    message[512]; // the engine's own detail when code != 0
} AnyLibStartResult;
```

Both buffers are always NUL-terminated, so a long message loses its tail rather than its terminator. Nothing here is heap-allocated and nothing needs freeing.

| `code` | Meaning |
|--------|---------|
| `0` | Up. `address` holds the bound host:port. |
| `1` | An instance is already running in this process — stop it first. |
| `2` | Bad data dir: empty, or not creatable. |
| `3` | Boot failed. `message` says why. |
| `4` | The search index could not be rebuilt: the boot removes an index this build cannot open and recreates it, and the removal or the fresh db failed too. Delete the account's index directory and retry; "reset local data" is the fallback. Only a start that boots an account already on disk returns it; a start that comes up unauthorized (managed, or standalone with no account) reports a failed rebuild on `POST /v1/auth` as `500 internal`. |
| `5` | Bad options: an unknown `mode`, `"managed"` without a `controlToken`, or a `nodeconfYAML` that isn't YAML or names no `networkId`. A host bug — never retryable. |
| `6` | The disk is full: the data dir, the account dir or a store could not be written. Free space and retry. A full disk is `6` even when it broke the data dir or an index rebuild; on `POST /v1/auth` it is `507 storage.no_space`. |
| `7` | A page of the account's SDK store failed its checksum while opening, in the quick check on a dirty open or a later read. It is never repaired in place: remove `sdk.db` and the files named after it from `<dataDir>/<accountId>/sdk/` (the `accountId` that `POST /v1/auth` returned) and start again. The SDK replays the synced trees into a fresh store; what lived only in `sdk.db` is lost: the local store, local-scope values and the files bookkeeping (durable files download again, files not yet durable are gone). On `POST /v1/auth` it is `500 sdk.store_corrupt`. |

New codes are only ever added: treat a code you don't know like `3`.

`nodeconfYAML` selects the network: an empty string means the production any-sync network embedded in the archive; pass a nodeconf's YAML text to join another ([Networks](networks.html)). `pushPeerId` / `pushAddrs` configure the push node — empty strings with an empty `nodeconfYAML` select the production push node; with any other `nodeconfYAML` they turn push off ([Push](../notifications/push.html)).

## Start it

```swift
import AnyLib

final class AnyBackend {
    static let shared = AnyBackend()
    private(set) var baseURL: URL!

    func start() throws {
        let dataDir = FileManager.default
            .urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("any").path

        // The parameters are `char *`, not `const char *`, so Swift won't
        // bridge a String for you. NULL reads as "" on the Go side.
        let dir = strdup(dataDir), addr = strdup("127.0.0.1:0")   // ":0" → OS picks a port
        defer { free(dir); free(addr) }

        var res = AnyLibStart(dir, addr, nil, nil, nil)        // nil nodeconf and push pair = production
        guard res.code == 0 else {
            throw NSError(domain: "any", code: Int(res.code),
                          userInfo: [NSLocalizedDescriptionKey: read(&res.message)])
        }
        baseURL = URL(string: "http://\(read(&res.address))/v1")!
    }

    func stop() { AnyLibStop() }

    /// Fixed-size `char[]` fields arrive as Swift tuples; rebind to read them.
    private func read<T>(_ field: inout T) -> String {
        withUnsafePointer(to: &field) {
            $0.withMemoryRebound(to: CChar.self, capacity: MemoryLayout<T>.size) {
                String(cString: $0)
            }
        }
    }
}
```

Build the URL from `address` rather than interpolating a port — that is the address the listener actually bound.

Code `4` deserves its own branch: the boot removes a search index this build cannot read and recreates it, so `4` means that rebuild itself failed (the removal, or the fresh index db). Deleting the account's index directory (`<dataDir>/<accountId>/index`) and retrying is the targeted recovery — the index is a derived cache, so nothing syncable is lost; "reset local data" is the fallback when that fails too.

Use Application Support (excluded from iCloud backup if you prefer) as the data dir; it holds the wallet, databases, files, and index for the account ([Data dir](../operations/data-dir.html)).

## Create or restore the account

A fresh data dir boots *unauthorized* — and a managed start boots unauthorized on every launch. Onboard over HTTP — `POST /v1/auth` with `{}` generates an account and returns the mnemonic **once**; with `{"mnemonic": "…"}` it restores one (same account id, fresh device key):

```swift
var req = URLRequest(url: AnyBackend.shared.baseURL.appendingPathComponent("auth"))
req.httpMethod = "POST"
req.setValue("application/json", forHTTPHeaderField: "content-type")
req.httpBody = "{}".data(using: .utf8)
let (data, _) = try await URLSession.shared.data(for: req)
// {"accountId":"A8g1…","created":true,"mnemonic":"w1 … w12"}  ← show the phrase, then never again
```

Until an account is booted, every route except `/v1/health`, `/v1/shutdown`, `/v1/openapi.json`, `/v1/auth` returns `401 auth.required` ([Accounts](../auth/accounts.html)).

## Then it is just HTTP

```swift
struct Create: Encodable { let type: String; let initialProperties: [String: [String: String]] }

var req = URLRequest(url: base.appendingPathComponent("spaces/\(space)/objects"))
req.httpMethod = "POST"
req.setValue("application/json", forHTTPHeaderField: "content-type")
// "page": the built-in document type
req.httpBody = try JSONEncoder().encode(Create(type: "page", initialProperties: ["any": ["name": "From iOS"]]))
let (data, _) = try await URLSession.shared.data(for: req)   // {"objectId":"bafy…"}
```

Live reads are a streaming POST: `URLSession.bytes(for:)` yields `AsyncSequence` lines — split frames on blank lines, `event:` / `data:` per line ([Subscriptions](../realtime/subscribe.html)).

```swift
let (bytes, _) = try await URLSession.shared.bytes(for: subscribeRequest)
for try await line in bytes.lines { /* accumulate until "" then dispatch on event */ }
```

## Lifecycle notes

- **One instance per process.** A second `AnyLibStart` while running fails; stop first.
- **Background expiry.** Call `AnyLibStopNow()` from a deadline-bounded task expiration handler; `AnyLibStop()` drains open streams with a 10 s ceiling.
- **Push.** Start with the push node and cache each space's `SpaceInfo.push` keys in a shared-access-group keychain item so the Notification Service Extension can decrypt while the server is not running ([Push](../notifications/push.html)).

> **Why it matters.** The app is not a client of a remote database — it *is* the database. Every screen reads local, indexed rows; the network is only ever the thing that brings other people's changes in.
