## Tool Description

Author and edit **mini apps** — small interactive React widgets stored in the space (type `mini_app`, content in the `mini_app` dataset: one record with `source` / `state` / `readme` fields). Each mini app is addressed by its `name` slug (lowercase, no-spaces, e.g. `"coin-flipper"`), which is also its display name. State is persistent — a mini app behaves like a long-lived page that remembers where it was.

Pick the right verb for the granularity:

- **First save** → `createMiniApp`.
- **Whole-block rewrite** → `updateMiniApp`.
- **Small source change** → `editMiniApp`.
- **State only** → `setState(name, obj)` / `upsertMiniAppState` (raw JSON).
- **Readme only** → `upsertReadme`.
- **Read** → `getMiniApp`, `getMiniAppSource`, `getState`, `listMiniApps`.

Slug forgotten? `listMiniApps()` returns every mini app's `name` + `title`. `createMiniApp` documents the source shape and author-time conventions.

## Tool Schema

### createMiniApp(opts) [mutator]

Create a new mini app. Fails if one with the same `name` already exists (use `updateMiniApp` to modify).

**Source shape.** The `source` is a short HTML snippet: a mount point (`<div id="app"></div>`) and a `<script>` that builds your React tree and calls `ReactDOM.createRoot(...).render(h(App))`. Three things come pre-loaded into the iframe — you don't add them yourself:
- `React` and `ReactDOM` globals.
- `useAnytypeState(initial)` — **same shape as React's `useState`**, but values it returns are persistent across reloads. Use this instead of `React.useState` for any state you want remembered. Plain `React.useState` is still available for ephemeral UI-only state (hover, animation flags, input drafts).

**No JSX** (no build step) — use `React.createElement` directly, aliasing as `h` by convention. **No external CDNs** — only the injected React runtime is available.

**Input** (`opts` object):
- `name` (string, required) — addressing slug AND display name. Lowercase, no spaces (e.g. `"coin-flipper"`).
- `source` (string, required) — the HTML snippet described above.
- `state` (object | string, optional) — initial state. Object is JSON.stringify'd; string is written as-is.
- `readme` (string, optional) — markdown notes stored alongside the source.

**Output:** `{ ok: true, name, title, id, object, warnings? }` on success. `warnings` lists any runtime scripts auto-injected because the source didn't include them (typical; don't treat as an error). `{ ok: false, error }` on failure.

**Example:**

```js
miniapp.createMiniApp({
  name: "counter",
  source:
    '<div id="app"></div>\n' +
    '<script>\n' +
    '  var h = React.createElement;\n' +
    '  function App() {\n' +
    '    var s = useAnytypeState({ count: 0 });\n' +
    '    var state = s[0], setState = s[1];\n' +
    '    function inc(){ setState({ count: (state.count || 0) + 1 }); }\n' +
    '    return h("button", { onClick: inc }, "Count: " + (state.count || 0));\n' +
    '  }\n' +
    '  ReactDOM.createRoot(document.getElementById("app")).render(h(App));\n' +
    '</script>',
  state: { count: 0 },
  readme: "Click the button to increment a counter."
});
```

### updateMiniApp(opts) [mutator]

Whole-block update of an existing mini app. Any field omitted is preserved. Runtime-script guard re-runs on `source`.

**Input:**
- `name` (string, required) — the mini app to update.
- `source` (string, optional) — full HTML replacement.
- `state` (object | string, optional) — state replacement.
- `readme` (string, optional) — readme replacement.
- `title` (string, optional) — AVOID: it renames the object, and the object name is the addressing slug — subsequent lookups by the old `name` will fail.

**Output:** `{ ok, name, object, warnings? }` on success, `{ ok: false, error }` on failure.

**Example:**

```js
miniapp.updateMiniApp({
  name: "counter",
  state: { count: 0, lastReset: "2026-04-22" }
});
```

### editMiniApp(name, opts) [mutator]

Surgical str_replace on a mini app's source or state block. Mirror of `anyPrograms.editProgram`. Block selector required only if you want to edit the state block — defaults to `"source"`.

When editing the source block, the React script guard re-runs after the replacement (so removing the script tag and re-saving will re-inject it).

**Input:**
- `name` (string, required).
- `opts.block` (string, optional, default `"source"`) — `"source"` or `"state"`.
- `opts.oldString` (string, required) — exact substring; must match exactly once unless `replaceAll: true`.
- `opts.newString` (string, required) — replacement; must differ from `oldString`.
- `opts.replaceAll` (boolean, optional, default `false`).

**Output:**
- On success: `{ ok: true, name, block, replacements, lengthBefore, lengthAfter, object }`.
- On failure: `{ ok: false, name, block?, error, lengthBefore? }`.

**Example:**

```js
miniapp.editMiniApp("counter", {
  oldString: '"Count: " + (state.count || 0)',
  newString: '"Clicks: " + (state.count || 0)'
});

miniapp.editMiniApp("counter", {
  block: "state",
  oldString: '"count": 0',
  newString: '"count": 100'
});
```

### setState(name, stateObject) [mutator]

Replace the JSON state block with the serialized form of `stateObject`. The common path for state writes from outside the running mini app (e.g. seeding, reset, programmatic mutation).

**Input:**
- `name` (string, required).
- `stateObject` (object, required) — must be JSON-serializable.

**Output:** `{ ok, name, object }` on success, `{ ok: false, name, error }` on failure.

**Example:**

```js
miniapp.setState("counter", { count: 0 });
```

### getState(name) [getter]

Return the parsed state object, or `null` if the mini app or its state block is missing or unparseable.

**Input:**
- `name` (string, required).

**Output:** the parsed JSON object, or `null`.

**Example:**

```js
var s = miniapp.getState("counter");
// { count: 7 }
```

### upsertReadme(name, newReadme) [mutator]

Replace the readme content (the `readme` field of the mini app record). Pass `""` to clear it.

**Input:**
- `name` (string, required).
- `newReadme` (string, required) — markdown content; no heading needed.

**Output:** `{ ok, name, object }` on success, `{ ok: false, name, error }` on failure.

**Example:**

```js
miniapp.upsertReadme("counter", "A simple click counter. State persists across reloads.");
```

### upsertMiniAppState(name, source) [mutator]

Write the JSON state block from a raw string. Use this when you already have the serialized JSON, or when the mini app was created without a state block and you want to insert one.

For typed object input prefer `setState`.

**Input:**
- `name` (string, required).
- `source` (string, required) — raw JSON text written verbatim into the `state` field.

**Output:** `{ ok, name, created, object }` — `created: true` if the state block was inserted, `false` if replaced.

**Example:**

```js
miniapp.upsertMiniAppState("counter", '{\n  "count": 42,\n  "label": "answers"\n}');
```

### getMiniApp(name, opts?) [getter]

Return the full mini app record. Optionally slice the source to a 1-indexed inclusive line range (`{ from, to }`). State is returned parsed (or as the raw string if unparseable).

**Input:**
- `name` (string, required).
- `opts.from` (number, optional) — first source line.
- `opts.to` (number, optional) — last source line (clamped to total).

**Output:** `{ id, name, title, source, state, readme, range? }` or `null`. `range: { from, to, totalLines }` is present only when sliced.

**Example:**

```js
var app = miniapp.getMiniApp("counter");
app.source;   // full HTML
app.state;    // { count: 7 }
app.readme;   // "A simple click counter..."

var head = miniapp.getMiniApp("counter", { from: 1, to: 20 });
head.source;  // first 20 lines of the source
head.range;   // { from: 1, to: 20, totalLines: 84 }
```

### getMiniAppSource(name, opts?) [getter]

Source-only convenience over `getMiniApp` — same line-range slicing. Cheaper to read for big apps when you only need the JS/HTML.

**Input:**
- `name` (string, required).
- `opts.from` (number, optional).
- `opts.to` (number, optional).

**Output:** `{ name, source, range? }` or `null`.

**Example:**

```js
miniapp.getMiniAppSource("counter", { from: 30, to: 50 });
// { name: "counter", source: "<lines 30..50>", range: { from: 30, to: 50, totalLines: 84 } }
```

### listMiniApps() [getter]

List every mini app in the space, sorted by `name`.

**Output:** `Array<{ id, name, title }>`.

**Example:**

```js
miniapp.listMiniApps();
// [{ id: "...", name: "counter", title: "Counter" }, ...]
```
