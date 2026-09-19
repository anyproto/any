---
title: Writing a program
description: File layout, the docstring convention, tool declaration with @span, result-shape rules and the import allowlist.
order: 20
---
# Writing a program

A program is one Python file whose docstring is its documentation, whose `main(args)` is its entry point, and whose public `@span` functions are its tool surface. Deploy and the agent's own program writer both check the convention and reject violations, so the rules below are a contract rather than a style guide.

## Layout and naming

| Form | Path | Use |
|---|---|---|
| flat | `programs/<name>@vN.py` | cron jobs, small tools |
| folder | `programs/<name>@vN/program.py` | larger tools; tests and fixtures ride alongside and deploy ignores them |

These paths are relative to a repo folder; `anyrt run --programs` selects the directory containing the versioned programs.

The name is a valid identifier; the version is `@vN`, explicit and exact — there is no floating `latest`. A published overlay version is **frozen**: an edit ships as `name@vN+1`. Programs in your own working space are editable in place.

## The module docstring is the doc

```python
"""Progress bars for long jobs — start/tick/done/fail, one bar per (space, job).

Programs and cells report progress ONLY through this module; never
hand-roll process events. Callers own the throttle: tick per work
chunk / percentage step, never per item.
"""
```

- First line: a self-contained one-liner, **≤ 80 chars**. It becomes the program's cached `summary` property, and every listing shows exactly that line.
- Whole docstring ~6 lines by convention; hard cap **12 lines / 800 chars**. A tool's docstring enters the standing prompt, so deploy rejects an overlong one.
- Developer context goes in `#` comments below the docstring, not in it.

There is no `description.md` or `schema.md`. `help(mod)` in the guest renders exactly what you wrote.

## Tools: `__any_tool__` and `@span`

A program the agent may call as a tool declares it at module top level and tags every public function:

```python
__any_tool__ = True


@span(kind="getter")  # noqa: F821 - guest global
def jobs(space):
    """Bars for a space → [{job, label, status, current, total, …}].

    Reads the server process view filtered to this space's jobs.
    status ∈ running | done | failed | cancelled."""
    ...


@span(kind="mutator")
def start(space, job, label, total=0, current=0, detail="", program=""):
    """Begin (or resume) a job's bar → process id."""
    ...
```

- `@span(kind=...)` on every public function; the call form is required (never bare `@span`). The recorded name defaults to `<module>.<function>`; an explicit first argument (`@span("progress.start", kind="mutator")`) is a deliberate display override.
- `kind` ∈ `getter` (read), `mutator` (write / side-effecting), `setup` (returns a stateful handle). It is narrative — never a capability gate.
- Public `def` = visible in the tool inventory; a leading underscore hides it. `main` is the entry point, not a method.
- Function docstrings: first line = the summary (the only line inventories show); body = the return shape, options, budgets.
- Module top level is **defs and constants only** — no effects at import time. Tool modules are executed on every run to compose the prompt.

Deploy rejects a program marked `__any_tool__ = True` whose module docstring is missing or over the caps, or that has no public `@span` function. A program that is not a tool (a trigger payload, a loop internal) sets `__any_tool__ = False` or omits it.

## Result shapes: trim, don't rename

Kept result fields carry the upstream API's exact names and nesting (`html_url`, `updated_at`, `user.login`; GraphQL camelCase verbatim). Trim to a small subset and cap string lengths, but never translate names — the model's prior is the upstream documentation, and a renamed field costs a discovery turn or a silent `.get()` `None`. Derived keys only where upstream has no scalar (`repo`, `is_pr`, decoded `text`), and every method names its return shape in the docstring (`→ {ok, path, text}`).

## What you can import

The guest never imports the host. Sources are executed inside the wasm kernel with the kernel names provided as globals: `use`, `effect`, `EffectError`, `span`, `http`, `blob` / `Blob`, `now`, `rand`, `env`, `uuid4`, `instant`, `ts_s`, `fmt_ts`, `tz_offset`, `values`, `effects`, `describe`, `inferSchema`, `subcell`, plus `print` and `help`. A cell that assigns, deletes or redefines one of them at module scope fails with `ReservedNameError` naming the line. Plain `import` serves the tiers baked into the kernel:

| Tier | Modules |
|---|---|
| pure stdlib | data and text (`json`, `re`, `string`, `textwrap`, `unicodedata`, `difflib`, `csv`, `html`, `email`, `xml.etree`, `urllib.parse`, `tomllib`, …), numbers (`math`, `decimal`, `fractions`, `statistics`, `calendar`, …), containers (`itertools`, `functools`, `collections`, `contextlib`, `dataclasses`, `enum`, `typing`, …), bytes (`base64`, `struct`, `zlib`, `gzip`, `zipfile`, `tarfile`, `hashlib`, `hmac`, …), `uuid` / `random` / `secrets` (seeded per run), `inspect`, `ast` |
| vendored pure-Python | `bs4` + `soupsieve`, `markdownify` |
| proxied | `datetime` and `time` (the present is a `time.now` record, `sleep` an effect), `os` (`os.environ` via `env.get`), `io` (no file openers), `tempfile` (a temporary file becomes a `Blob`), `sqlite3` (`:memory:` only), `mimetypes` (built-in table) |

Modules that would open a side door — `pathlib`, `socket`, `subprocess`, `threading`, `asyncio`, `urllib.request`, `pickle`, … — raise `ImportError` naming where the capability actually lives; anything else unlisted raises `ImportError` naming the boundary. Other programs are loaded with `use("name@vN")`, never imported — see [Modules and overlays](modules-and-overlays.html).

Curated builtins exclude `open`, `input`, `eval`, `exec`, `compile`, `breakpoint`, `globals`, `locals`, `vars`. `help(obj)` is available and prints `describe(obj)` — the same renderer the prompt uses.

## Cross-repo dependencies

Inside a repo, `use("llm@v1")` resolves in the repo's own space. A dependency on another repo is alias-qualified — `use("agent:progress@v1")`.

## Agent-authored programs

The agent can write programs into the working space through the `programs@v1` tool (`create_program` / `update_program` / `edit_program` / `delete_program`). The write path runs deploy's checks and is stricter, because no human reviews the source — docstring present and within caps for every program, valid `name@vN`, syntax gate, import allowlist scan, and for tools `__any_tool__` plus `@span` and a docstring on every public module-level def — and a post-save probe `use()`s the result: a program that fails to load stays saved with `any_tool: false` and the call returns `{ok: false, saved: true, hint: …}`. Writes refuse a `name@vN` that a joined overlay exports, so shipped code is never shadowed by accident.

> **Note.** A saved program grants no capability a cell doesn't already have — the write path adds persistence, not power. Every authored program runs through the same boundary, budget and secrets rules as a deployed one.

## Checklist before deploy

- Docstring first line ≤ 80 chars; whole docstring ≤ 12 lines / 800 chars.
- `main(args)` present for anything a trigger runs.
- `__any_tool__ = True` ⇒ `@span(kind=…)` and a docstring on every public function.
- No effects at module top level.
- Only allowlisted imports; no `open`, no threads, no `async`; never rebind a kernel name.
