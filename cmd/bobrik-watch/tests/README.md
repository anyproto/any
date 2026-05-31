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
   anytype-agent-runtime cmd/bobrik-watch/tests/engine_string_semantics.js
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

## Takeaway for the agent / docs

Authoring a regex in program source: double-escape inside the string
(`[\\s\\S]`, `<\\/item>`), or `new RegExp(stringWithDoubledBackslashes)`. The
`anyPrograms` tool docs and the codegen system prompt should say so, and ideally
flag "works in run_cell, fails in stored program → suspect string escaping, not
the engine."
