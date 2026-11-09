---
title: Android
description: Embed the any server inside an Android app process with any.aar — start it on a loopback port, read the bound address, and talk HTTP to it from the app.
order: 60
---
# Android

On Android the server does not run as a separate process — it is a library. `any.aar` is a gomobile binding of the same server; your app starts it in-process, gets a loopback address back, and uses plain HTTP from there.

## Get the artifact

Every release publishes `any.aar` (arm64-v8a) alongside the desktop tarballs, with its sha256 in the release notes. Build it yourself with an Android NDK on the path:

```bash
make build-android            # → dist/android/any.aar
```

Drop it into `app/libs/` and add it as a dependency:

```kotlin
// build.gradle.kts
dependencies {
    implementation(files("libs/any.aar"))
}
```

## The surface

The binding is deliberately flat — top-level functions over strings, plus one result type:

| Function | Purpose |
|----------|---------|
| `Mobile.startWithResult(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs, mode, controlToken) -> StartResult` | Boot the server. Returns once the listener is bound or the boot failed, and never throws: `code` says which. `mode` `""` (or `"standalone"`) boots the account on disk inside the start. `"managed"` makes the app the owner: the account arrives over `POST /v1/auth` on every launch, keys never touch disk, and sign-out / switch / shutdown need `controlToken` (`X-Any-Control-Token`). |
| `Mobile.address()` | The actually bound `host:port`. Differs from `listenAddr` when you passed `:0`. Empty when not running. |
| `Mobile.stop()` | Graceful shutdown; waits for the server to exit. Safe when not running. |
| `Mobile.stopNow()` | Hard stop, returns promptly — for a deadline-bounded teardown. |
| `Mobile.version()` | Build-stamped version string. |

`nodeconfYAML` is the network: pass `""` for the production any-sync network embedded in the AAR, or a nodeconf's YAML text to join another one ([Networks](networks.html)). `pushPeerId` / `pushAddrs` (comma-separated) name the push node; empty strings with an empty `nodeconfYAML` select the production push node; with any other `nodeconfYAML` they turn push off ([Push](../notifications/push.html)). The AAR runs the index with no embedder, so `/search` runs `fts` only.

`StartResult` carries `code`, `address` (the bound `host:port` when `code` is `0`) and `message` (the engine's own detail otherwise). The codes are the iOS shim's:

| `code` | Meaning |
|--------|---------|
| `0` | Up. `address` holds the bound host:port. |
| `1` | An instance is already running in this process — stop it first. |
| `2` | Bad data dir: empty, or not creatable. |
| `3` | Boot failed. `message` says why. |
| `4` | The boot could not rebuild a search index it refused. Delete the account's index directory (`<dataDir>/<accountId>/index`) and retry — the index is a derived cache, so nothing syncable is lost; "reset local data" is the fallback. |
| `5` | Bad options: an unknown `mode`, `"managed"` without a `controlToken`, or a `nodeconfYAML` that isn't YAML or names no `networkId`. A host bug — never retryable. |
| `6` | The disk is full: the data dir, the account dir or a store could not be written. Free space and retry. A full disk is `6` even when it broke the data dir or an index rebuild. |
| `7` | The account's SDK store failed its integrity check on a dirty open. It is never repaired in place: remove `sdk.db` and the files named after it from `<dataDir>/<accountId>/sdk/` (the `accountId` that `POST /v1/auth` returned) and start again. The SDK replays the synced trees into a fresh store; what lived only in `sdk.db` is lost: the local store, local-scope values and the files bookkeeping (durable files download again, files not yet durable are gone). |

Codes `4` and `7` come only from a start that boots an account already on disk; `6` also covers the data dir. A start that comes up unauthorized (managed, or no account yet) reports the account's failures on `POST /v1/auth` ([Accounts](../auth/accounts.html)).

## Start it

```kotlin
import io.anyproto.any.mobile.Mobile

class AnyService : Service() {
    override fun onCreate() {
        super.onCreate()
        Thread {
            // "127.0.0.1:0": the OS picks a free port; "" nodeconf and push pair = production; "" mode = standalone
            val res = Mobile.startWithResult(filesDir.absolutePath, "127.0.0.1:0", "", "", "", "", "")
            if (res.code != 0) {
                showLaunchError(res.code, res.message)               // one branch per code above
                return@Thread
            }
            AnyClient.baseUrl = "http://${res.address}/v1"            // e.g. 127.0.0.1:41823
        }.start()
    }
    override fun onDestroy() { Mobile.stop(); super.onDestroy() }
}
```

Use `filesDir` (or another app-private directory) as the data dir: it holds the wallet, the databases, files, and the search index for the account ([Data dir](../operations/data-dir.html)).

## Create or restore the account

An embedded server starts *unauthorized* on a fresh data dir — there is no `any init` on a phone. Onboard over HTTP:

```kotlin
// generate a new account; the mnemonic comes back ONCE — show it and make the user save it
POST http://$addr/v1/auth      {}
// → { "accountId": "A8g1…", "created": true, "mnemonic": "w1 … w12" }

// or restore from a phrase (same account id, fresh device key)
POST http://$addr/v1/auth      { "mnemonic": "w1 … w12" }
```

Until then every route except `/v1/health`, `/v1/shutdown`, `/v1/openapi.json`, `/v1/auth` answers `401 auth.required`. On later launches a standalone server finds the wallet in `filesDir` and boots the engine on `startWithResult`; a managed one waits for `POST /v1/auth` again ([Accounts](../auth/accounts.html)).

## Then it is just HTTP

```kotlin
val client = OkHttpClient()
// "page": the built-in document type
val body = """{"type":"page","initialProperties":{"any":{"name":"From Android"}}}"""
val req = Request.Builder()
    .url("${AnyClient.baseUrl}/spaces/$space/objects")
    .post(body.toRequestBody("application/json".toMediaType()))
    .build()
client.newCall(req).execute().use { println(it.body!!.string()) }   // {"objectId":"bafy…"}
```

Live reads are the same streaming POST as everywhere else — read the response body line by line and split on blank lines ([JavaScript](javascript.html) shows the parser; the frame set is in [Subscriptions](../realtime/subscribe.html)).

## Lifecycle notes

- **One server per process.** A second start while running answers code `1`; restart on the same data dir is serialized by `stop`.
- **Background limits.** Use `stopNow()` from a deadline-bounded callback; `stop()` drains open streams with a 10 s ceiling.
- **Push.** Pass the push node to `startWithResult` and cache each space's `SpaceInfo.push` keys in the Keystore so a notification extension can decrypt while the server is not running ([Push](../notifications/push.html)).

> **Why it matters.** The phone holds the whole database. There is no "mobile API" that returns less than the desktop one, no offline cache to invalidate, and no request that fails because the network is down — the app talks to `127.0.0.1` and sync happens whenever it can.
