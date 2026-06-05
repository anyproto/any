## Tool Description

Author, run, and **edit** programs and tools that live in the `any` space. A "program" is a JS module saved as a `program`-typed object (built-in type; source in the `program_source` dataset). A "tool" is a program with `program.any_tool === true`, which auto-binds it as a kernel global on the next runtime boot. Tool docs are stored SPLIT: the `## Tool Description` body in `program_description`, one record per `### method(sig) [kind]` in `program_methods` — `saveProgram` does the split when you pass full markdown; `any_tool` is set true iff both halves are present. `createProgram` always saves source and tool docs — there is no plain-program path there.

Pick the right verb for the granularity:

- **Whole-file rewrite** (major refactor, first save) → `createProgram` / `updateProgram`.
- **Small source change** (fix a string literal, add a header, rename a variable) → `editProgram`. Mirror of `anyHelper.editObject`.
- **Doc tweak** (rewrite a method's description, add a new method's docs) → `upsertDescription` / `upsertMethodDescription`. Section-aware — you never re-emit the whole markdown.
- **Read current state** → `getProgram` for the full record; `getProgramDescription` / `getMethodDescription` for just a section.

### Tool-JS conventions (REQUIRED reading before calling createProgram)

The runtime is Sobek (Go-embedded JS engine), not Node.js. Every tool source MUST follow these rules:

1. **Program name must be a valid JS identifier** (`[A-Za-z_$][A-Za-z0-9_$]*`). Hyphens, dots, and leading digits are rejected by `saveProgram` — the boot prelude emits `var <name>;` and would otherwise crash with `Unexpected token`.
2. **One required export: `export function main(args)`.** This is the entry point invoked when the runtime executes the program directly. It receives an `args` object built from CLI key=value pairs. Even if your tool's primary surface is named exports (most tools), `main` must exist.
3. **Named exports are the public API.** Anything you want callable from another tool's kernel — including this one — must be `export function name(...)`. After saving, the tool will surface those names via its facade (auto-bound as a kernel global with `tool.listMethods()` and `tool.describeMethod(name)`).
4. **Imports use `name@version` form.** `import { createClient } from "anyHelper@v1";`. The `@v1` suffix is required. The version comes from the tool record's `programVersion` field.
5. **No Node.js APIs.** No `require`, no `process`, no `fs`, no `Buffer`, no `setTimeout`/`setInterval` outside what Sobek provides. `console.log` works and writes to traces. `fetch` is **synchronous** and auto-parses JSON bodies (`resp.body` is the parsed object).
6. **No `async`/`await`.** Sobek doesn't run an event loop. All I/O is synchronous: `var resp = fetch(url, opts);`.
7. **Globals available to every tool:** `env` (object — environment variables), `console`, `fetch`. Inside `toolcall_core` kernels, `js.eval` and every other tool is pre-bound as a global by name.
8. **`markdown` MUST include `## Tool Description` and `## Tool Schema` sections.** `saveProgram` rejects the write otherwise. Examples should look like the call would look from inside a kernel where the tool is already bound — e.g. `webSearch.search("query")`, not `import { search } from ...`. The single exception is `anyPrograms.createProgram` documentation itself, where examples teach how to author imports in source.
9. **Method headings in `## Tool Schema` use the form `### name(args) [kind]`** where kind is one of `getter`, `mutator`, `setup`, or `program`. Methods tagged `[program]` are hidden from the discovery surface (they stay callable on the underlying object but don't appear in `listMethods` / `describeMethod`).

### Worked example: a tiny tool

```js
import { createClient } from "anyHelper@v1";

var _client = null;
function _c() {
  if (_client) return _client;
  _client = createClient({
    apiBaseUrl: env.ANYTYPE_API_URL,
    apiKey: env.ANYTYPE_API_KEY,
    spaceId: env.ANYTYPE_SPACE_ID
  });
  return _client;
}

export function countObjects(typeKey) {
  var objs = _c().getObjects(typeKey);
  return { ok: true, type: typeKey, count: objs.length };
}

export function main(args) {
  return countObjects(args.typeKey || "page");
}
```

Saved with markdown describing `countObjects(typeKey)`, this becomes `objectCounter.countObjects("page")` from any kernel after the next boot.

## Tool Schema

### listPrograms() [getter]
List all programs in the space. Returns [{id, name, version, title, anyTool}].

### getProgram(name, version?) [getter]
Get a program's full record. Returns {id, name, version, title, source, description, methods, markdown}.
- name: program name
- version: version string (default "v1")
- `description` is the Tool Description body; `methods` is the ordered method-doc list `[{name, bareName, kind, text, pos}]`; `markdown` is a back-compat alias of `description` (method docs are NOT in it).

### createProgram(opts) [mutator]
Create a new program.
- opts.name: program name (required, must be a valid JS identifier)
- opts.source: JS source code (required)
- opts.markdown: tool docs containing `## Tool Description` and `## Tool Schema` (required for tools)
- opts.version: version (default "v1")

`opts.source` is a string, so a plain template literal eats backslashes the same
way any JS string does (`` `[\s\S]` `` becomes `[sS]`, `` `\d` `` becomes `d`) —
which silently breaks regexes in the saved program. When the source contains
regexes or backslashes, wrap it in `String.raw` so they survive verbatim:

```js
anyPrograms.createProgram({
  name: "techCrunchNews",
  source: String.raw`
import { createClient } from "anyHelper@v1";
export function fetchNews() {
  var re = /<item[^>]*>([\s\S]*?)<\/item>/g;   // single backslashes — preserved as written
  // ...
}
export function main(args) { return fetchNews(); }
`,
  markdown: "## Tool Description\n...\n## Tool Schema\n..."
});
```

(Caveat: a `String.raw` template can't contain an unescaped backtick or `${` — rare in program source; escape just those if needed.)

### updateProgram(opts) [mutator]
Update an existing program's source.
- opts.name: program name (required)
- opts.source: new source code (required)
- opts.markdown: replace tool docs (optional — when given, full markdown with both `## Tool Description` and `## Tool Schema`; re-split on save)
- opts.version: version (default "v1")

### saveProgram(opts) [mutator]
Low-level save both createProgram and updateProgram route through. Two modes:
- source-only (no `markdown`): writes `program_source`, leaves docs and `any_tool` untouched.
- tool save (`markdown` given): splits the markdown, rewrites `program_description` + `program_methods` (stale method records are deleted), sets `any_tool: true`. The markdown must carry a non-empty `## Tool Description` AND a `## Tool Schema` with at least one `### method()` subsection.

Prefer `createProgram`/`updateProgram` — they add main-export validation and an import probe; `saveProgram` is the escape hatch when you need to skip those.
- opts.name (required, valid JS identifier), opts.source (required)
- opts.markdown (optional, full tool md; `schema` accepted as a legacy alias)
- opts.version (default "v1"), opts.title (default name)

**Output:** `{ ok, object: { id }, name, version }` or `{ ok: false, error }`.

### runProgram(name, args, version?) [mutator]
Execute a program.
- name: program name
- args: arguments object
- version: version string (default "v1")

### editProgram(programName, opts) [mutator]
Surgical string replacement on program source. Source-only — tool docs and `any_tool` are never touched by this path.
- programName: program name
- opts.oldString, opts.newString, opts.replaceAll

### upsertDescription(programName, newDescription, opts?) [mutator]

Replace — or create if missing — the program's Tool Description (the `program_description` dataset record). `any_tool` is recomputed after the write (true iff the description is non-empty AND at least one method doc exists).

**Input:**
- `programName` (string, required).
- `newDescription` (string, required) — the description body (no `## Tool Description` heading line; just the content).
- `opts.version` (string, optional, default `"v1"`).

**Output:** `{ ok, name, version, created, object }` — `created: true` if there was no description before.

**Example:**

```js
anyPrograms.upsertDescription("wikipedia",
  "Wraps the Wikipedia REST API. Requires a User-Agent header.\n\nNo auth."
);
```

### upsertMethodDescription(programName, methodName, newMethodDescription, opts?) [mutator]

Replace — or create if missing — one method's doc (a `program_methods` record). When the method exists, its heading (`name`), `kind`, and order are preserved; only the body is replaced. When it's new, it's appended at the tail — pass a full heading as `methodName` (e.g. `"topStories(limit) [getter]"`) to set the signature and kind; a bare name gets `name()` and kind `getter`. `any_tool` is recomputed after the write.

**Content shape:** `newMethodDescription` should be free-form markdown with at least an `**Input:**` section listing parameters as `- name (type) — description` bullets, plus `**Output:**` and `**Example:**` blocks.

**Input:**
- `programName` (string, required).
- `methodName` (string, required) — bare method name to address an existing method; full heading form to create with signature/kind.
- `newMethodDescription` (string, required) — body content for the section (no heading line).
- `opts.version` (string, optional, default `"v1"`).

**Output:** `{ ok, name, version, methodName, created, object }` — `created: true` if a new method record was inserted.

**Example:**

```js
anyPrograms.upsertMethodDescription("hackerNews", "topStories",
  "Returns the top `limit` HN stories.\n\n" +
  "**Input:**\n" +
  "- `limit` (number, optional, default 10) — how many stories.\n\n" +
  "**Output:** `[{ id, title, url, score, by }]`.\n\n" +
  "**Example:** `hackerNews.topStories(5)`"
);
```

### getProgramDescription(programName, opts?) [getter]

Return the Tool Description body as a plain string (no heading line). Returns `null` if the program is not found or has no description.

**Input:**
- `programName` (string, required).
- `opts.version` (string, optional, default `"v1"`).

**Output:** `string | null`.

**Example:**

```js
var desc = anyPrograms.getProgramDescription("hackerNews");
// "Wraps the Hacker News Firebase API..."
```

### getMethodDescription(programName, methodName, opts?) [getter]

Return the full method section (heading line plus body) as a plain string, or `null` if the method isn't documented.

**Input:**
- `programName` (string, required).
- `methodName` (string, required) — the bare method name.
- `opts.version` (string, optional, default `"v1"`).

**Output:** `string | null`.

**Example:**

```js
var section = anyPrograms.getMethodDescription("hackerNews", "topStories");
// "### topStories(limit) [getter]\n\nReturns the top `limit` HN stories.\n..."
```
