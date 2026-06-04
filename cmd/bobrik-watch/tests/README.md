# bobrik-watch integration tests

Integration tests for the program-storage pipeline, plus the engine probe that
explains a class of agent bugs around regexes in program source.

## Why this exists

A bobrik-watch run spent ~14 turns failing to build a TechCrunch RSS-scraper
program. The symptom: a regex like `/<item[^>]*>([\s\S]*?)<\/item>/g` matched
fine when typed into `run_cell`, but the *stored program* parsed 0 items, then
threw `Cannot read property 'replace'/'trim' of null`. The agent misattributed
it to "the isolated runtime handles regex/CDATA differently" and brute-forced
out with extra backslashes + blanket null-guards.

The real cause has two halves, and these tests pin both:

1. **Engine (expected, standard JS).** Program source is built as a JS string —
   usually a backtick template literal — and *that string* is what's stored.
   Inside any JS string literal an unrecognized escape drops its backslash
   (`"\s" === "s"`), so `[\s\S]` written inside `` `...` `` becomes `[sS]`,
   which matches only the letters s/S. A regex *literal* (`/[\s\S]/`) is parsed
   by the regex grammar and keeps its backslashes — hence the run_cell vs.
   stored-program asymmetry. This is ordinary ECMAScript, not a bug.

   Reproduce:

   ```
   ./bin/any-agent-runtime cmd/bobrik-watch/tests/engine_string_semantics.js
   ```

   `fromTemplateLiteral` / `fromDoubleQuoted` → `[sS]*?</item>` (the trap);
   `fromDoubledEscape` / `fromRegexLiteralSource` → `[\s\S]…` (intact).

2. **Server (must be lossless).** Once a string reaches the server, the
   create → `/modify` → storage → `/query` round-trip must return it
   byte-for-byte — no escaping, no markdown mangling (old anytype had a
   markdown-escaping pass; `any` does not, and this test guards against one
   creeping back in). The `program` type uses plain `DefaultHandler`s and raw
   `$set` ops, so there is nothing to strip. `program_roundtrip_test.go` proves
   it across backslashes, quotes, CDATA brackets, unicode, JSON, and markdown.

## Running

The Go test needs a live server (one account = one data dir = one port):

```
go build ./cmd/any
ANY_DATA_DIR=/tmp/any-it ./any init       # first run only
ANY_DATA_DIR=/tmp/any-it ./any run        # foreground, :7001
go test -tags integration ./cmd/bobrik-watch/tests/ -run Program -v
```

It creates (and reuses) an `integration_test` space. Point at another server
with `ANY_ADDR=127.0.0.1:7002`. With no server reachable the test `t.Skip`s
rather than failing.

## JS-level tests (anyHelper / anyPrograms)

`program_roundtrip_test.go` pins the *server*; `jsrunner_test.go` pins the JS
*client* layered on top — it runs the real Anytype JS engine against the live
server and asserts on what the JS methods actually return (the contract
bobrik-watch's programs depend on).

Each JS test lives in `tests/js/*_test.js`, exports `main(args)` with
`{ apiBaseUrl, spaceId }`, runs assertions through a small inline harness, and
prints exactly one line as its last act:

```
HARNESS_RESULT {"pass":N,"fail":M,"failures":[...]}
```

`jsrunner_test.go` (`TestJSAnyHelper`) discovers those files, builds this
repo's **`cmd/any-agent-runtime`** (same engine, but module resolution wired
through `internal/anyrt` — the production anySDK loader), and execs it on
each test. Imports resolve **space-first** exactly like production (so a
program a test saves, e.g. `createProgram`'s import probe, is importable
immediately), with the `cmd/bobrik-watch` dir as `-m` file fallback for
modules not synced into the test space. It parses the HARNESS_RESULT line and
fails the Go subtest when `fail > 0` or the line is missing. It `t.Skip`s
when the server is unreachable; no PATH setup needed — the runtime builds
from this repo.

```
ANY_ADDR=127.0.0.1:7003 \
  go test -tags integration ./cmd/bobrik-watch/tests/ -run TestJSAnyHelper -v
```

> Gotcha: don't name a Go test file `*_js_test.go` — `js` is a valid `GOOS`
> (the WASM target), so Go gives it an implicit `GOOS=js` build constraint and
> silently excludes it on every other platform. The runner is `jsrunner_test.go`
> for this reason.

## Takeaway for the agent / docs

Authoring a regex in program source: double-escape inside the string
(`[\\s\\S]`, `<\\/item>`), or `new RegExp(stringWithDoubledBackslashes)`. The
`anyPrograms` tool docs should say so, and ideally
flag "works in run_cell, fails in stored program → suspect string escaping, not
the engine."
