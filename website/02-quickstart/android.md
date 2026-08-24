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

The binding is deliberately flat — top-level functions, strings and errors only:

| Function | Purpose |
|----------|---------|
| `Mobile.start(dataDir, listenAddr, nodeconfYAML)` | Boot the server. Returns once the listener is bound; throws on wallet / SDK / listener failure with the underlying message. A second call while running throws. |
| `Mobile.startWithPush(dataDir, listenAddr, nodeconfYAML, pushPeerId, pushAddrs)` | Same, plus the push-notification node (`pushAddrs` comma-separated). Empty strings = no push. |
| `Mobile.address()` | The actually bound `host:port`. Differs from `listenAddr` when you passed `:0`. Empty when not running. |
| `Mobile.stop()` | Graceful shutdown; waits for the server to exit. Safe when not running. |
| `Mobile.stopNow()` | Hard stop, returns promptly — for a deadline-bounded teardown. |
| `Mobile.version()` | Build-stamped version string. |

`nodeconfYAML` is the network: pass `""` for the production any-sync network embedded in the AAR, or a nodeconf's YAML text to join another one ([Networks](networks.html)). The AAR builds with the full-text search leg; the vector leg is off, so `/search` runs `fts` only.

## Start it

```kotlin
import mobile.Mobile

class AnyService : Service() {
    override fun onCreate() {
        super.onCreate()
        Thread {
            Mobile.start(filesDir.absolutePath, "127.0.0.1:0", "")   // OS picks a free port
            val addr = Mobile.address()                              // e.g. 127.0.0.1:41823
            AnyClient.baseUrl = "http://$addr/v1"
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

Until then every route except `/v1/health`, `/v1/shutdown`, `/v1/openapi.json`, `/v1/auth` answers `401 auth.required`. On later launches the wallet in `filesDir` is found and the engine boots on `start` ([Accounts](../auth/accounts.html)).

## Then it is just HTTP

```kotlin
val client = OkHttpClient()
val body = """{"types":["page"],"initialProperties":{"any":{"name":"From Android"}}}"""
val req = Request.Builder()
    .url("${AnyClient.baseUrl}/spaces/$space/objects")
    .post(body.toRequestBody("application/json".toMediaType()))
    .build()
client.newCall(req).execute().use { println(it.body!!.string()) }   // {"objectId":"bafy…"}
```

Live reads are the same streaming POST as everywhere else — read the response body line by line and split on blank lines ([JavaScript](javascript.html) shows the parser; the frame set is in [Subscriptions](../realtime/subscribe.html)).

## Lifecycle notes

- **One server per process.** `start` while running throws; restart on the same data dir is serialized by `stop`.
- **Background limits.** Use `stopNow()` from a deadline-bounded callback; `stop()` drains open streams with a 10 s ceiling.
- **Push.** Pass the push node to `startWithPush` and cache each space's `SpaceInfo.push` keys in the Keystore so a notification extension can decrypt while the server is not running ([Push](../notifications/push.html)).

> **Why it matters.** The phone holds the whole database. There is no "mobile API" that returns less than the desktop one, no offline cache to invalidate, and no request that fails because the network is down — the app talks to `127.0.0.1` and sync happens whenever it can.
