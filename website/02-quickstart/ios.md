---
title: iOS
description: Embed the any server in an iOS app with any.xcframework — a C-archive with four exported functions — then talk HTTP to it on loopback.
order: 70
---
# iOS

On iOS the server is a static library. `any.xcframework` wraps a Go c-archive (device + simulator slices) exporting a handful of C functions; your app starts the server in-process and uses the normal HTTP API from Swift.

## Get the artifact

Every release publishes `any.xcframework.zip`, sha256-pinned in the release notes. Building it requires macOS with Xcode's iOS SDK:

```bash
scripts/build-xcframework.sh dist/ios      # → dist/ios/any.xcframework.zip
```

Unzip and add `any.xcframework` to your target as an embedded binary. The archive builds with the full-text search leg; vector search and the local embedder are off on iOS, so `/search` runs `fts` only.

## The surface

The header exposes four functions. Strings are C strings; `AnyServerStart` returns the bound **port** on success or a negative error code:

| Function | Purpose |
|----------|---------|
| `AnyServerStart(dataDir, listenAddr, nodeconfYAML, indexEnabled) -> int` | Boot the server. Returns the bound port (≥ 0) once the listener is up; negative on failure (already running, bad data dir, boot error). |
| `AnyServerStartWithPush(dataDir, listenAddr, nodeconfYAML, indexEnabled, pushPeerId, pushAddrs) -> int` | Same, plus the push node. Empty strings = no push. |
| `AnyServerStop()` | Graceful shutdown; waits for the server to exit. Safe when not running. |
| `AnyServerStopNow()` | Hard stop, returns promptly — for `applicationWillTerminate` or an expiring background task. |

`nodeconfYAML` selects the network: an empty string means the production any-sync network embedded in the archive; pass a nodeconf's YAML text to join another ([Networks](networks.html)). `indexEnabled` lets a share extension skip the search indexer entirely.

## Start it

```swift
import AnyServer

final class AnyBackend {
    static let shared = AnyBackend()
    private(set) var baseURL: URL!

    func start() throws {
        let dataDir = FileManager.default
            .urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("any").path
        let port = AnyServerStart(dataDir, "127.0.0.1:0", "", true)   // ":0" → OS picks a port
        guard port >= 0 else { throw NSError(domain: "any", code: Int(port)) }
        baseURL = URL(string: "http://127.0.0.1:\(port)/v1")!
    }

    func stop() { AnyServerStop() }
}
```

Use Application Support (excluded from iCloud backup if you prefer) as the data dir; it holds the wallet, databases, files, and index for the account ([Data dir](../operations/data-dir.html)).

## Create or restore the account

A fresh data dir boots *unauthorized*. Onboard over HTTP — `POST /v1/auth` with `{}` generates an account and returns the mnemonic **once**; with `{"mnemonic": "…"}` it restores one (same account id, fresh device key):

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
struct Create: Encodable { let types: [String]; let initialProperties: [String: [String: String]] }

var req = URLRequest(url: base.appendingPathComponent("spaces/\(space)/objects"))
req.httpMethod = "POST"
req.setValue("application/json", forHTTPHeaderField: "content-type")
req.httpBody = try JSONEncoder().encode(Create(types: ["page"], initialProperties: ["any": ["name": "From iOS"]]))
let (data, _) = try await URLSession.shared.data(for: req)   // {"objectId":"bafy…"}
```

Live reads are a streaming POST: `URLSession.bytes(for:)` yields `AsyncSequence` lines — split frames on blank lines, `event:` / `data:` per line ([Subscriptions](../realtime/subscribe.html)).

```swift
let (bytes, _) = try await URLSession.shared.bytes(for: subscribeRequest)
for try await line in bytes.lines { /* accumulate until "" then dispatch on event */ }
```

## Lifecycle notes

- **One server per process.** A second `AnyServerStart` while running fails; stop first.
- **Background expiry.** Call `AnyServerStopNow()` from a deadline-bounded task expiration handler; `AnyServerStop()` drains open streams with a 10 s ceiling.
- **App Sandbox helpers on macOS** use the `-sandbox` desktop tarball rather than this archive ([Builds and CI](../operations/builds-and-ci.html)).
- **Push.** Start with the push node and cache each space's `SpaceInfo.push` keys in a shared-access-group keychain item so the Notification Service Extension can decrypt while the server is not running ([Push](../notifications/push.html)).

> **Why it matters.** The app is not a client of a remote database — it *is* the database. Every screen reads local, indexed rows; the network is only ever the thing that brings other people's changes in.
