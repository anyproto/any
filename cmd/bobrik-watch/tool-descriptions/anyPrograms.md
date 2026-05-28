## Tool Description

Author, run, and **edit** programs and tools that live in the `any` space. A "program" is a JS module saved as a `program`-typed object (built-in type, datasets `program_source` / `program_description` / `program_methods`); a "tool" is a program whose `program_description` is non-empty, which auto-binds it as a kernel global on the next runtime boot. `createProgram` always saves both source and tool docs — there is no plain-program path here.

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

### listPrograms()
List all programs in the space. Returns [{id, name, version, title}].

### getProgram(name, version?)
Get a program's source code. Returns {id, name, version, source}.
- name: program name
- version: version string (default "v1")

### createProgram(opts)
Create a new program.
- opts.name: program name (required, must be a valid JS identifier)
- opts.source: JS source code (required)
- opts.markdown: tool docs containing `## Tool Description` and `## Tool Schema` (required for tools)
- opts.version: version (default "v1")

### updateProgram(opts)
Update an existing program's source.
- opts.name: program name (required)
- opts.source: new source code (required)
- opts.markdown: replace tool docs (optional)
- opts.version: version (default "v1")

### runProgram(name, args, version?)
Execute a program.
- name: program name
- args: arguments object
- version: version string (default "v1")

### editProgram(programName, opts)
Surgical string replacement on program source.
- programName: program name
- opts.oldString, opts.newString, opts.replaceAll
