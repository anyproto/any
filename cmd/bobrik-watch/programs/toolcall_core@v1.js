// __main_source
// toolcall_core@v1 — single-tool agent using Anthropic's native tools API.
//
// Architecture: one tool exposed to the model — `run_cell(code)`. Each turn the
// model emits one or more tool_use blocks; the harness translates each to a
// js.eval call against the persistent kernel and returns lastValue + traces as
// a tool_result block. Conversation history accumulates as native messages, so
// the model sees its own prior code (in tool_use blocks) and prior results
// (in tool_result blocks) without manual step-log rendering.
//
// Termination: Anthropic's stop_reason === "end_turn" — when the model is done,
// it emits an assistant message containing only text (no tool_use). The agent
// loop checks stop_reason and returns the text as the final answer.
//
// Run: anytype-agent-runtime -m assistantjs assistantjs/toolcall_core@v1.js text="..."

import { createClient, getProp } from "anyHelper@v1";
import { createLLM } from "llm@v1";
import { displayValue, inferSchema, formatTraceOneLiner, summarizeTrace } from "utils@v1";
import { createConvMemory } from "convmemory@v1";
// No tracer import: Sonnet recovers from errors directly via is_error tool_result.
// The tracer added LLM calls for marginal benefit when the model is capable
// enough to fix its own broken cells in the next turn.

var llm = createLLM();

// ============================================================================
// CONSTANTS
// ============================================================================

// Per-VALUE inline budget. A console.log value or the cell's Last value is
// rendered inline (full, via displayValue) when it fits this; past it, we show
// a `[<N> chars, schema <...> — logs.get("<id>", <sel>) to walk]` stub and stash
// the structured value in the kernel-side `_valueStore` for a follow-up cell to
// fetch via logs.get(toolUseId, idx). Keeps huge structures out of context
// while leaving them queryable. ~4k chars ≈ 1k tokens: generous enough that a
// genuinely small result (a short list, a computed aggregate) inlines and the
// model reads it directly; past it, the size+schema stub + logs.get round-trip
// is cheaper than dumping the structure into every turn's context.
var MAX_VALUE_INLINE_CHARS = 4000;

// Conversation-history windows. History lives in structured datasets ON THE
// CHAT OBJECT (agent_turns / agent_chunks — docs/11-agent-memory.md): raw
// turns are append-only and kept forever; the boot context is bounded by
// these query limits, NOT by trimming storage. Compression produces chunk
// summaries with explicit fromSeq..toSeq pointers back into the raw turns —
// it never rewrites or deletes anything.
var TURNS_TO_INJECT = 8;        // raw turns rendered into the boot window
var CHAT_CHUNKS_TO_INJECT = 8;  // compressed chunks injected above the window

// Count-based compression trigger: when more than TURNS_PER_CHUNK + LIVE_TAIL
// turns sit above the last chunk's toSeq, the oldest TURNS_PER_CHUNK-sized
// overflow is summarized into a new chunk; the newest LIVE_TAIL turns always
// stay un-summarized so the live window keeps raw fidelity.
var TURNS_PER_CHUNK = 10;
var LIVE_TAIL = 6;

// Space-Context split thresholds. The Main space_context object is injected
// into every system prompt; when its body crosses SPACE_CTX_SPLIT_CHARS we run
// a post-turn LLM pass that redistributes content across Main + child
// space_context objects. The target is well below the threshold so a single
// long append doesn't re-trigger the split on the next turn.
var SPACE_CTX_SPLIT_CHARS = 30000;
var SPACE_CTX_TARGET_CHARS = 18000;

// ============================================================================
// Tool definition — single Anthropic tool
// ============================================================================
var RUN_CELL_TOOL = {
  name: "run_cell",
  description: "Execute a JavaScript cell in a persistent kernel. " +
    "**`run_cell` is the ONLY tool you have.** Every operation — searching the web, " +
    "querying or mutating Anytype, anything else — happens by writing JS code that " +
    "references pre-bound kernel globals. Names like `anyHelper`, `webSearch`, " +
    "`anyPrograms` are JavaScript objects bound INSIDE the kernel; they are NOT " +
    "separate Anthropic tools. Calling them as tool_use blocks (e.g. `tool_use{name: \"webSearch\"}`) " +
    "will fail. Always wrap in run_cell: `run_cell({code: \"webSearch.search('...')\"})`. " +
    "The kernel state (variables, functions, imports) survives across all run_cell calls within " +
    "this request — every cell runs in the SAME runtime instance, so a `var x = ...` in one " +
    "cell is accessible from the next. " +
    "Cell semantics (Jupyter-style): top-level statements, NOT a function. " +
    "DO NOT write `function main(args) { ... }`, DO NOT use `return`, DO NOT use `import` statements. " +
    "Use `var` for bindings you want to keep across calls. " +
    "The cell's last expression is captured as the cell result. " +
    "READING RESULTS — the tool_result has up to three sections: **Output** (everything you " +
    "`console.log`, in order — this is your primary way to see data, so log exactly what you want " +
    "to inspect), **Last value** (the cell's final expression), and **Side Effects** (a one-line " +
    "summary of API calls made). console.log freely; nothing prints to a user. A logged value or " +
    "the Last value that is large is NOT shown in full — it collapses to " +
    "`[<N> chars, schema <...> — logs.get(\"toolu_...\", <i>) to walk]`. To inspect it, call " +
    "`logs.get(\"toolu_...\", i)` (numeric index from the Output line) or `logs.get(\"toolu_...\", \"last\")` " +
    "in a later cell — it returns the real structured value; slice/filter/inferSchema it like any JS. " +
    "For the full Side Effects trace, `toolEffects.get(\"toolu_...\")`. " +
    "When you have completed the user's request, stop calling run_cell and respond with the final answer as plain text.",
  input_schema: {
    type: "object",
    properties: {
      code: {
        type: "string",
        description: "The JavaScript cell source. Top-level `var` declarations persist across cells. The last expression value is returned. Must NOT contain `import` or `function main()`."
      }
    },
    required: ["code"]
  }
};

// ============================================================================
// Helpers — tool discovery, boot prelude, and dynamic system prompt section
// ============================================================================

// Render one stored method record back to its markdown section — the shape
// describeMethod returns. The kind tag is re-attached to the heading
// (`### name(sig) [kind]`), matching how the doc was authored.
function _renderMethodDoc(m) {
  var heading = "### " + m.name + (m.kind ? " [" + m.kind + "]" : "");
  return m.text ? heading + "\n\n" + m.text : heading;
}

// Build the per-session toolDocs table from getTools() results. Docs come
// from the split datasets via getToolDocs (program_description = description
// body, program_methods = one record per method) — no markdown parsing.
// Hard-errors if the anyHelper tool is not discoverable in the space — every
// kernel must have it as the `anyHelper` global.
function _buildToolDocs(bootClient, tools) {
  var toolDocs = {};
  for (var ti = 0; ti < tools.length; ti++) {
    var t = tools[ti];
    // Route the dataset reads to the program's source space. Without this,
    // a system-space tool's id won't resolve from the user-space client.
    var docs = bootClient.getToolDocs(t.id, t.space ? { space: t.space } : undefined);
    var methods = docs.methods;

    // For anyHelper specifically, hide [program]-tagged methods from the
    // discovery surface. They stay callable on the underlying instance, just
    // not enumerated by listMethods / describeMethod.
    if (t.programName === "anyHelper") {
      methods = methods.filter(function(m) { return m.kind !== "program"; });
    }

    toolDocs[t.programName] = {
      toolId: t.id,
      programName: t.programName,
      programVersion: t.programVersion || "v1",
      description: docs.description,
      createdDate: "",
      methods: methods.map(function(m) { return { bareName: m.bareName, signature: m.name, content: _renderMethodDoc(m) }; })
    };
  }
  if (!toolDocs["anyHelper"]) {
    throw new Error("anyHelper not found in getTools() — every kernel boot requires it. Ensure the anyHelper tool is saved to the space and tagged any_tool.");
  }
  return toolDocs;
}

// For anyHelper specifically, additional method names that exist on the
// instance but should be hidden from the discovery surface. The .md may not
// document them at all (e.g. getToolDocs — boot plumbing); we still want
// them out of listMethods.
var ANYHELPER_HIDDEN_METHODS = [
  "getToolDocs",
  "fetchTrace",
  "fetchTraceSchema"
];

// Build the boot prelude that goes into js.eval. Generates one var-decl per
// tool, with the __makeToolFacade factory wrapping each tool's namespace (or,
// for anyHelper, the createClient(args) instance) into an object that
// exposes the real methods plus listMethods()/describeMethod(name).
function _buildBootPrelude(toolDocs) {
  var docsJson = JSON.stringify(toolDocs);
  var hiddenJson = JSON.stringify({ anyHelper: ANYHELPER_HIDDEN_METHODS });
  var lines = [];
  lines.push('var __ns_anyHelper = __import("anyHelper@v1");');
  lines.push('var __helperInstance = __ns_anyHelper.createClient(args);');
  lines.push('var __ns_utils = __import("utils@v1");');
  lines.push('var inferSchema = __ns_utils.inferSchema;');
  lines.push('var __toolDocs = ' + docsJson + ';');
  lines.push('var __hiddenMethods = ' + hiddenJson + ';');
  lines.push(
    'var __makeToolFacade = function(programName, methodSourceObj) {\n' +
    '  var docs = __toolDocs[programName];\n' +
    '  var hidden = __hiddenMethods[programName] || [];\n' +
    '  var docNames = docs.methods.map(function(m){return m.bareName;});\n' +
    '  var exportNames = [];\n' +
    '  for (var k in methodSourceObj) {\n' +
    '    if (typeof methodSourceObj[k] !== "function") continue;\n' +
    '    if (hidden.indexOf(k) !== -1) continue;\n' +
    '    if (k.indexOf("__") === 0) continue;\n' +  // double-underscore = harness-facing (e.g. __prepareTraces)
    '    exportNames.push(k);\n' +
    '  }\n' +
    '  var allNames = exportNames.slice();\n' +
    '  for (var i = 0; i < docNames.length; i++) {\n' +
    '    if (allNames.indexOf(docNames[i]) === -1) allNames.push(docNames[i]);\n' +
    '  }\n' +
    '  var facade = {};\n' +
    '  for (var k2 in methodSourceObj) facade[k2] = methodSourceObj[k2];\n' +
    '  facade.listMethods = function() { return allNames.slice(); };\n' +
    '  facade.describeMethod = function(name) {\n' +
    '    for (var i = 0; i < docs.methods.length; i++) {\n' +
    '      if (docs.methods[i].bareName === name) return docs.methods[i].content;\n' +
    '    }\n' +
    '    if (typeof methodSourceObj[name] === "function" && hidden.indexOf(name) === -1) {\n' +
    '      return "Method \'" + name + "\' is callable on " + programName + " but not documented in its markdown.";\n' +
    '    }\n' +
    '    return "Unknown method \'" + name + "\' on " + programName + ". Available: " + allNames.join(", ");\n' +
    '  };\n' +
    '  return facade;\n' +
    '};'
  );
  lines.push('var anyHelper = __makeToolFacade("anyHelper", __helperInstance);');

  var failures = [];
  for (var name in toolDocs) {
    if (name === "anyHelper") continue;
    var version = toolDocs[name].programVersion || "v1";
    var spec = name + "@" + version;
    var nsVar = "__ns_" + name.replace(/[^a-zA-Z0-9_]/g, "_");
    lines.push(
      'var ' + name + ';\n' +
      'try {\n' +
      '  var ' + nsVar + ' = __import(' + JSON.stringify(spec) + ');\n' +
      '  ' + name + ' = __makeToolFacade(' + JSON.stringify(name) + ', ' + nsVar + ');\n' +
      '} catch (__importErr_' + nsVar + ') {\n' +
      '  ' + name + ' = null;\n' +
      '  console.log("[boot] tool ' + name + ' failed to import: " + String(__importErr_' + nsVar + '));\n' +
      '}'
    );
  }

  // Chain every tool's optional __prepareTraces hook. Tools own their own
  // filter logic (anyHelper drops fetches to its host, webSearch drops
  // fetchBatch to Gemini/Vertex, …); this iterator stays tool-agnostic so new
  // tools self-register by exporting __prepareTraces with no harness edits.
  //
  // Per-tool isolation: a hook may only mutate its own `<name>.*` keys plus
  // the shared infra keys (`fetch`, `fetchBatch`, `js.eval`). Any change it
  // tries to make to another tool's keys is reverted from the pre-call state.
  // Without this, a buggy hook (e.g. one that does `if (k.indexOf("foo.") === 0) continue;`
  // and forgets to keep the entry) would silently wipe other tools' traces
  // from the visible Effects digest.
  lines.push(
    'var __SHARED_TRACE_KEYS = { fetch: 1, fetchBatch: 1, "js.eval": 1 };\n' +
    'var __prepareToolTraces = function(traces) {\n' +
    '  var out = traces;\n' +
    '  for (var __n in __toolDocs) {\n' +
    '    var __t = globalThis[__n];\n' +
    '    if (!__t || typeof __t.__prepareTraces !== "function") continue;\n' +
    '    var __prefix = __n + ".";\n' +
    '    var __before = out;\n' +
    '    var __hook;\n' +
    '    try { __hook = __t.__prepareTraces(__before); } catch (e) { continue; }\n' +
    '    if (!__hook || typeof __hook !== "object") continue;\n' +
    '    var __merged = {};\n' +
    '    var __k;\n' +
    '    for (__k in __before) {\n' +
    '      if (!__before.hasOwnProperty(__k)) continue;\n' +
    '      var __isOwn = (__k.indexOf(__prefix) === 0) || __SHARED_TRACE_KEYS[__k];\n' +
    '      if (__isOwn) {\n' +
    '        if (__hook.hasOwnProperty(__k)) __merged[__k] = __hook[__k];\n' +
    '        // else hook intentionally dropped its own key — honor it.\n' +
    '      } else {\n' +
    '        __merged[__k] = __before[__k];\n' +
    '      }\n' +
    '    }\n' +
    '    for (__k in __hook) {\n' +
    '      if (!__hook.hasOwnProperty(__k)) continue;\n' +
    '      if (__merged.hasOwnProperty(__k)) continue;\n' +
    '      var __isOwn2 = (__k.indexOf(__prefix) === 0) || __SHARED_TRACE_KEYS[__k];\n' +
    '      if (__isOwn2) __merged[__k] = __hook[__k];\n' +
    '    }\n' +
    '    out = __merged;\n' +
    '  }\n' +
    '  return out;\n' +
    '};'
  );

  // Per-toolUseId effect store. The host stashes a cell's full Side Effects
  // trace one-liner array here; cells fetch it back via toolEffects.get(id)
  // (returns array of one-liner strings) or list all stashed ids via
  // toolEffects.list(). Cleared by js.reset() at the start of each
  // runToolcaller invocation.
  lines.push('globalThis._toolEffectsStore = {};');
  lines.push(
    'globalThis.toolEffects = {\n' +
    '  get: function(id) { return globalThis._toolEffectsStore[id] || null; },\n' +
    '  list: function() { return Object.keys(globalThis._toolEffectsStore); }\n' +
    '};'
  );

  // Per-toolUseId structured-value store. The host always stashes a cell's
  // console.log values (in call order) plus its Last value here; when a value
  // is too big to inline in the tool_result, the digest shows a size+schema
  // stub pointing at logs.get(id, idx). Cells fetch the real structured value
  // back via logs.get(id, i) for a numeric log index, logs.get(id, "last") for
  // the Last value, or logs.get(id) for the whole log array — then inspect with
  // normal JS. Cleared by js.reset() alongside _toolEffectsStore.
  lines.push('globalThis._valueStore = {};');
  lines.push(
    'globalThis.logs = {\n' +
    '  get: function(id, i) {\n' +
    '    var e = globalThis._valueStore[id];\n' +
    '    if (!e) return null;\n' +
    '    if (i === undefined) return e.logs;\n' +
    '    if (i === "last") return e.last;\n' +
    '    return e.logs[i];\n' +
    '  },\n' +
    '  list: function() { return Object.keys(globalThis._valueStore); }\n' +
    '};'
  );

  return { code: lines.join("\n"), importFailures: failures };
}

// Build the dynamic "Tools available in this kernel" section of the system
// prompt. Per tool: ## heading + verbatim Tool Description + bare method names.
function _buildToolsPromptSection(toolDocs) {
  // Ordering discipline for the injected tool list:
  //   1. `anyHelper` pinned first.
  //   2. `convmemory` pinned second.
  //   3. Everything else by Anytype `created_date` ascending (oldest first),
  //      so stable tools bubble up above newer, churning ones. Undated tools
  //      sort last. Alphabetical as final tiebreak.
  // ISO 8601 dates with `Z` suffix sort correctly as strings — the format
  // is fixed-width left-to-right most-significant, no parsing needed.
  var PIN_ORDER = { "anyHelper": 0, "convmemory": 1 };
  var names = Object.keys(toolDocs).sort(function(a, b) {
    var ap = PIN_ORDER[a], bp = PIN_ORDER[b];
    if (ap !== undefined && bp !== undefined) return ap - bp;
    if (ap !== undefined) return -1;
    if (bp !== undefined) return 1;
    var ad = (toolDocs[a] && toolDocs[a].createdDate) || "";
    var bd = (toolDocs[b] && toolDocs[b].createdDate) || "";
    if (!ad && bd) return 1;   // undated → last
    if (ad && !bd) return -1;
    if (ad < bd) return -1;
    if (ad > bd) return 1;
    return a < b ? -1 : a > b ? 1 : 0;
  });
  var globalsList = names.map(function(n) { return "- " + n; }).join("\n");
  var out = "## Pre-bound globals in the run_cell kernel\n\n" +
    "The names below are JavaScript objects pre-bound as globals INSIDE the run_cell kernel — they are NOT Anthropic tools you can invoke directly. Use them only by writing JS that references them, e.g. `webSearch.search(\"...\")`, inside a `run_cell({code: \"...\"})` call. Never emit a tool_use block named after one of them — only `run_cell` is a real tool.\n\n" +
    globalsList + "\n" +
    "- inferSchema     (utility — returns a shape/preview of any value: full container structure, big strings collapsed to a 200-char head + remaining-size)\n\n" +
    "## Module reference\n\n";
  for (var i = 0; i < names.length; i++) {
    var n = names[i];
    var t = toolDocs[n];
    out += "### " + n + "\n\n";
    out += (t.description && t.description.length > 0 ? t.description : "(no description in tool's md)") + "\n\n";
    var methodNames = t.methods.map(function(m) { return m.signature || m.bareName; });
    if (methodNames.length > 0) {
      out += "Methods: " + methodNames.join(", ") + "\n\n";
    } else {
      out += "Methods: (none documented — call " + n + ".listMethods() at runtime)\n\n";
    }
  }
  out +=
    "## Discovering method signatures — required before each call\n\n" +
    "These modules are pre-bound as globals; do NOT import them and do NOT call them as Anthropic tools. The method SIGNATURES above show argument NAMES only — not their accepted shapes, value kinds, return shape, or examples. A bare arg name like `typeOrQuery` or `opts` hides real structure (a string OR a `{filter, sort, limit}` query object, etc.); do NOT assume the simplest form. You do not need to call listMethods.\n\n" +
    "Before calling a method you have not used yet in this session, fetch its full inputs, outputs, and example with describeMethod (always inside a run_cell call):\n\n" +
    "```\n" +
    "var doc = anyHelper.describeMethod(\"createObject\");\n" +
    "doc\n" +
    "```\n\n" +
    "```\n" +
    "var doc = webSearch.describeMethod(\"search\");\n" +
    "doc\n" +
    "```\n\n" +
    "Once you have described a method this session, you don't need to describe it again. Guessing signatures wastes turns; describe first, call second.\n\n" +
    "(`<module>.listMethods()` is also available for parity, but the names are already in this prompt — there's no need to call it.)\n\n";
  return out;
}

// Fetch convmemory's current categories at boot and render them into a short
// system-prompt section. Gives the agent a live inventory (builtins + any
// invented categories already in the space) so it can pick category filters
// without running listCategories() as its first run_cell. Counts omitted —
// they'd invalidate prompt cache on every memory write without helping the
// agent pick filters. Uses js.eval against the already-booted kernel where
// `convmemory` is bound as a global. Fails silent (empty string) if it
// isn't available — e.g. not deployed yet, or an import error during boot.
function _fetchCategoriesSection() {
  try {
    var r = js.eval(
      "(function(){ try { if (typeof convmemory !== 'object' || !convmemory || typeof convmemory.listCategories !== 'function') return null; return JSON.stringify(convmemory.listCategories()); } catch(e) { return null; } })()",
      {}
    );
    if (!r || r.error || !r.result) return "";
    var parsed;
    try { parsed = JSON.parse(r.result); } catch (e) { return ""; }
    if (!parsed) return "";
    var builtin = parsed.builtin || [];
    var observed = parsed.observed || [];
    // Split observed into builtin vs. invented so the prompt flags the
    // invented ones — nudges the agent to reuse rather than invent new.
    var builtinSet = {};
    for (var bi = 0; bi < builtin.length; bi++) builtinSet[builtin[bi]] = true;
    var invented = [];
    for (var oi = 0; oi < observed.length; oi++) {
      var n = observed[oi].name;
      if (!builtinSet[n]) invented.push(n);
    }
    var lines = ["## Memory categories\n"];
    lines.push("Builtin: " + builtin.join(", ") + ".");
    if (invented.length > 0) {
      lines.push("Invented (already in use — REUSE before inventing new): " + invented.join(", ") + ".");
    }
    lines.push("\nUse `convmemory.memoryByCategory({categories: [...]})` to read them. NOTE: semantic (similarity-ranked) recall is not available yet — `convmemory.search` runs in degraded mode (period/category/recency only), so prefer explicit category and period reads.\n\n");
    return lines.join("\n");
  } catch (e) {
    return "";
  }
}


// ============================================================================
// Chat history — structured agent_turns / agent_chunks datasets on the chat
// object (docs/11-agent-memory.md). No anchor object, no markdown parsing:
// the chat object id IS the scope, the boot window is a bounded indexed
// query, and persistence is one append-only record per invocation.
// ============================================================================

// Resolve every usable display handle for a space member — typically two
// values: the stripped `global_name` (e.g. `anton.any` → `anton`) and the
// display `name` (e.g. `bobrik` or `Gallant Lark`). Ordered: globals first.
// Returns [] when the lookup fails so callers can detect "unknown member".
function resolveMemberHandles(client, spaceId, memberIdOrIdentity) {
  if (!memberIdOrIdentity || !spaceId) return [];
  var res;
  try { res = client.getSpaceMember(memberIdOrIdentity); } catch (e) { return []; }
  if (!res || res.ok === false) return [];
  var handles = [];
  var global = res.global_name ? String(res.global_name).replace(/\.any$/i, "").trim() : "";
  if (global) handles.push(global);
  var nm = res.name ? String(res.name).trim() : "";
  if (nm && handles.indexOf(nm) === -1) handles.push(nm);
  return handles;
}

// Backwards-compatible single-name resolver — used by sender display in
// turn records where one primary handle is enough. Returns null on failure.
function resolveMemberName(client, spaceId, memberIdOrIdentity) {
  var handles = resolveMemberHandles(client, spaceId, memberIdOrIdentity);
  return handles.length > 0 ? handles[0] : null;
}

// Escape a string for use inside a RegExp literal body.
function _reEscape(s) {
  return String(s).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Does `text` mention any of `handles` as a whole word? Matches case-insensitive
// `@handle` OR bare `handle` at word boundaries — Anytype strips the `@` prefix
// when users pick the structured mention affordance, so leaving it mandatory
// would miss every UI-driven mention.
function _textMentionsAny(text, handles) {
  if (!text || !handles || handles.length === 0) return false;
  for (var i = 0; i < handles.length; i++) {
    var h = handles[i];
    if (!h) continue;
    var re = new RegExp("(^|\\W)@?" + _reEscape(h) + "(?=\\W|$)", "i");
    if (re.test(text)) return true;
  }
  return false;
}

// Resolve the chat object's name (top-level obj.name — per feedback_no_obj_properties,
// we do NOT read obj.properties.*). Returns null on any failure.
function resolveChatName(client, chatId) {
  if (!chatId) return null;
  try {
    var obj = client.getObject(chatId, { resolveRefs: false });
    return (obj && obj.name) ? obj.name : null;
  } catch (e) { return null; }
}

// Decide whether the bot should respond to this message.
//   opts = { spaceType, chatId, botIdentity, text }
// Returns { respond, reason, chatName?, botHandle? }.
//
// Rules:
//   - text missing / blank                             → skip (no-op turn)
//   - spaceType === null (legacy call, no middleware)  → respond
//   - spaceType === 4 (OneToOne)                       → respond
//   - chat name starts with `bot-`                     → respond
//   - otherwise: respond iff text contains `@<botHandle>`
//     (bot handle resolved via GET /v1/spaces/{id}/members/{botIdentity})
function shouldRespond(client, opts) {
  if (!opts.text || !String(opts.text).trim()) {
    return { respond: false, reason: "empty-text" };
  }

  var st = opts.spaceType;
  if (st === null)  return { respond: true, reason: "no-spaceType" };
  if (st === 4)     return { respond: true, reason: "one-to-one" };

  var chatName = resolveChatName(client, opts.chatId);
  if (chatName && chatName.trim().toLowerCase().indexOf("bot-") === 0) {
    return { respond: true, reason: "bot-chat", chatName: chatName };
  }

  var botHandles = resolveMemberHandles(client, client.config && client.config.spaceId, opts.botIdentity);
  if (botHandles.length === 0) {
    return { respond: false, reason: "no-bot-handle", chatName: chatName };
  }
  var primary = botHandles[0];
  if (_textMentionsAny(opts.text, botHandles)) {
    return { respond: true, reason: "mention", chatName: chatName, botHandle: primary };
  }
  return { respond: false, reason: "no-mention", chatName: chatName, botHandle: primary };
}

// ============================================================================
// Space Context — singleton "Main" object (type `space_context`) whose markdown
// is injected into every system prompt. Child space_context objects hold
// overflow sections, listed as links in the prompt and fetched on-demand by
// the agent via anyHelper.getObject(id). Identity convention: Main has
// name === "Main"; any other name is a child. The feature is gated on the
// presence of the `_space_context` agent-skill — absent skill = no-op.
//
// The runtime only READS space_context objects. Bootstrap (type creation +
// empty Main) lives in deploy-assistant.sh so a runtime restart never rewrites
// or re-creates user content. If Main is missing at read time the feature
// renders a "Main is unavailable" placeholder instead of self-healing.
// ============================================================================

function loadSpaceContextMain(client) {
  var objects;
  try { objects = client.getObjects("space_context"); } catch (e) { return null; }
  if (!objects || objects.length === 0) return null;
  for (var i = 0; i < objects.length; i++) {
    var o = objects[i];
    if (o && o.name === "Main") {
      var full = client.getObject(o.id, { resolveRefs: false });
      return { id: o.id, markdown: (full && full.markdown) || "" };
    }
  }
  return null;
}

// Returns child space_context objects (name !== "Main") as [{id, name}]. No
// markdown is fetched — children are listed as links in the prompt and the
// agent pulls content on demand.
function getChildSpaceContexts(client) {
  var objects;
  try { objects = client.getObjects("space_context"); } catch (e) { return []; }
  if (!objects || objects.length === 0) return [];
  var out = [];
  for (var i = 0; i < objects.length; i++) {
    var o = objects[i];
    if (!o || o.name === "Main") continue;
    out.push({ id: o.id, name: o.name || "(untitled)" });
  }
  out.sort(function(a, b) { return a.name < b.name ? -1 : a.name > b.name ? 1 : 0; });
  return out;
}

// Render structured turn records to plain text for the compression
// summarizer. Same surface the old markdown transcript carried — user line,
// reply bubbles, effects one-liners — but produced from typed fields, never
// parsed back.
export function renderTurnsForSummary(turns) {
  var out = [];
  for (var i = 0; i < turns.length; i++) {
    var t = turns[i];
    var lines = ["### " + _unixToIso(t.createdAt)];
    if (t.userText) {
      lines.push(t.userName ? "user (@" + t.userName + ")> " + t.userText : "user> " + t.userText);
    }
    if (t.think) lines.push("think> " + t.think);
    var replies = t.replies || [];
    for (var ri = 0; ri < replies.length; ri++) {
      if (replies[ri]) lines.push("assistant> " + replies[ri]);
    }
    if (t.effects && t.effects.length > 0) {
      lines.push("effects>");
      for (var ei = 0; ei < t.effects.length; ei++) lines.push(t.effects[ei]);
    }
    out.push(lines.join("\n"));
  }
  return out.join("\n\n");
}

// Unix-seconds → ISO string ("" for missing/invalid).
function _unixToIso(unix) {
  if (!unix || typeof unix !== "number") return "";
  try { return new Date(unix * 1000).toISOString(); } catch (e) { return ""; }
}

// Pull {id, name, typeKey} out of a trace output blob (lastOut) with
// inputArgs as a fallback for fields that aren't in the response body.
// This keeps the trace → effects digest lossless enough to produce clickable
// links without a second API call — create/update/addTag responses all echo
// the target object with name+type.
function resolveObjectRef(lastOut, inputArgs) {
  var ref = { id: "", name: "", typeKey: "" };
  if (lastOut && lastOut.object) {
    ref.id = lastOut.object.id || "";
    ref.name = lastOut.object.name || "";
    if (lastOut.object.type) ref.typeKey = lastOut.object.type.key || "";
  }
  if (!ref.id && lastOut && lastOut.id) ref.id = lastOut.id;
  if (inputArgs && inputArgs.length > 0) {
    // createObject(typeKey, {...}) → args[0] is the typeKey string.
    // updateObject(id, {...}) / deleteObject(id) / addTag(id, name) → args[0] is the id.
    if (!ref.typeKey && typeof inputArgs[0] === "string" &&
        inputArgs[0].length < 40 && inputArgs[0].indexOf("bafy") !== 0) {
      ref.typeKey = inputArgs[0];
    }
    if (!ref.id && typeof inputArgs[0] === "string" && inputArgs[0].indexOf("bafy") === 0) {
      ref.id = inputArgs[0];
    }
  }
  return ref;
}

// Render an object reference as a clickable markdown link: any://spaceId/objectId
function objectLink(ref, spaceId) {
  var label = ref.name || ref.id || "?";
  if (!ref.id) return label;
  var url = spaceId ? "any://" + spaceId + "/" + ref.id : "any://_/" + ref.id;
  return "[" + label + "](" + url + ")";
}

// Extract compact effects-digest lines from a cell's trace map. Walks
// anyHelper.* mutation calls and emits one prefixed line per mutation.
// Lines carry the object type plus a clickable markdown link so the chat
// history is both machine-parseable and human-readable.
//   created <typeKey> [<name>](any://<sid>/<id>)
//   updated <typeKey> [<name>](any://<sid>/<id>)
//   deleted <typeKey> [<id>](any://...)      (name is gone post-delete)
//   tagged <tag> on [<name>](any://...)
// Read-only helpers (getObjects, getObject, search, listTypes, …) are skipped.
function extractEffects(traces, spaceId) {
  if (!traces) return [];
  var lines = [];
  for (var name in traces) {
    if (name.indexOf("anyHelper.") !== 0) continue;
    var bare = name.substring("anyHelper.".length);
    // Explicit allow-list — avoids emitting effects for read-only helpers.
    var op = null;
    if (bare === "createObject") op = "created";
    else if (bare === "updateObject") op = "updated";
    else if (bare === "deleteObject") op = "deleted";
    else if (bare === "addTag") op = "tagged";
    else if (bare === "removeTag") op = "untagged";
    else if (bare === "createType") op = "typed";
    // (saveProgram moved to anyPrograms — its inner client calls surface
    // here as createObject/setRecord, so program saves still show up.)
    else continue;

    var record = traces[name];
    for (var inputKey in record) {
      var outputs = record[inputKey];
      if (!outputs || outputs.length === 0) continue;
      var lastOut = outputs[outputs.length - 1];
      // The wrap serializes outputs as JSON strings — parse back to an object
      // so field access works. formatTraceOneLiner gets away without this
      // because it only calls inferSchema(lastOut).
      if (typeof lastOut === "string") {
        try { lastOut = JSON.parse(lastOut); } catch (e) { /* leave as string */ }
      }
      var inputArgs = null;
      try { inputArgs = JSON.parse(inputKey); } catch (e) {}

      if (op === "created" && lastOut && lastOut.ok && lastOut.object) {
        var ref = resolveObjectRef(lastOut, inputArgs);
        lines.push("created " + (ref.typeKey || "?") + " " + objectLink(ref, spaceId));
      } else if (op === "updated" && lastOut && lastOut.ok) {
        var refU = resolveObjectRef(lastOut, inputArgs);
        lines.push("updated " + (refU.typeKey || "?") + " " + objectLink(refU, spaceId));
      } else if (op === "deleted") {
        var refD = resolveObjectRef(lastOut, inputArgs);
        lines.push("deleted " + (refD.typeKey || "?") + " " + objectLink(refD, spaceId));
      } else if (op === "tagged" && lastOut && lastOut.ok) {
        var refT = resolveObjectRef(lastOut, inputArgs);
        var tagName = (inputArgs && inputArgs[1]) || lastOut.tag_key || "?";
        lines.push("tagged " + tagName + " on " + objectLink(refT, spaceId));
      } else if (op === "untagged") {
        var refUT = resolveObjectRef(lastOut, inputArgs);
        var utagName = (inputArgs && inputArgs[1]) || "?";
        lines.push("untagged " + utagName + " on " + objectLink(refUT, spaceId));
      } else if (op === "typed" && lastOut && lastOut.ok) {
        var tkey = (lastOut.type && lastOut.type.key) || (inputArgs && inputArgs[0] && inputArgs[0].key) || "?";
        lines.push("typed " + tkey);
      } else if (op === "program-saved" && lastOut && lastOut.ok) {
        var pn = lastOut.name || (inputArgs && inputArgs[0] && inputArgs[0].programName) || "?";
        var pv = lastOut.version || (inputArgs && inputArgs[0] && inputArgs[0].programVersion) || "v1";
        lines.push("program-saved " + pn + "@" + pv);
      }
    }
  }
  return lines;
}

// Compression: count-based. When more than TURNS_PER_CHUNK + LIVE_TAIL turns
// have accumulated above the last chunk's toSeq, summarize the oldest
// overflow (everything except the newest LIVE_TAIL turns) into one new chunk
// carrying explicit fromSeq..toSeq pointers. Turns are NEVER mutated or
// deleted — the pointers are the "compacted" marker, and the live window is
// bounded by the boot query's limit, not by storage size.
export function maybeCompressTurns(conv, chatObjId, newestSeq) {
  var last = null;
  try { last = conv.lastChunk(chatObjId); } catch (e) {}
  var fromSeq = last ? last.toSeq + 1 : 0;
  var uncompressed = newestSeq - fromSeq + 1;
  if (uncompressed < TURNS_PER_CHUNK + LIVE_TAIL) {
    return { skipped: true, uncompressed: uncompressed };
  }
  var toSeq = newestSeq - LIVE_TAIL;

  var turns;
  try { turns = conv.turnRange(chatObjId, fromSeq, toSeq); } catch (e) {
    return { skipped: true, error: "turn range load failed: " + (e && e.message ? e.message : String(e)) };
  }
  if (!turns || turns.length === 0) return { skipped: true };

  var prompt = "Summarize the following chat history slice into 5-8 sentences. " +
    "Preserve: (a) what the user asked for, (b) what objects were created/updated/deleted with their TYPES and NAMES and IDs when available, " +
    "(c) any unresolved threads or pending follow-ups, (d) any long-form artifact the assistant stashed in an Anytype object (include its name/type/id so a future turn can locate it). " +
    "Be terse but dense — this is a memory aid for a future assistant turn, not a narrative, and several of these summaries will be concatenated so each one must stand on its own. " +
    "Output plain text only, no headers, no markdown.\n\n" +
    "CHAT HISTORY SLICE:\n" + renderTurnsForSummary(turns);

  var summary = null;
  try {
    var resp = llm.chat([{ role: "user", content: prompt }], {});
    if (resp && resp.content) {
      var parts = [];
      for (var ci = 0; ci < resp.content.length; ci++) {
        if (resp.content[ci].type === "text") parts.push(resp.content[ci].text);
      }
      summary = parts.join("\n").trim();
    }
  } catch (e) {
    return { skipped: true, error: "compression LLM failed: " + (e && e.message ? e.message : String(e)) };
  }
  if (!summary) return { skipped: true, error: "compression produced empty summary" };

  var realToSeq = turns[turns.length - 1].seq;
  var res = conv.createChunk(chatObjId, {
    seq: last ? last.seq + 1 : 0,
    summary: summary,
    periodStart: turns[0].createdAt || 1,
    periodEnd: turns[turns.length - 1].createdAt || turns[0].createdAt || 1,
    fromSeq: turns[0].seq,
    toSeq: realToSeq,
    turnsCovered: turns.length
  });
  if (!res.ok) return { skipped: true, error: "createChunk failed: " + res.error };
  return { skipped: false, chunkSeq: res.seq, fromSeq: turns[0].seq, toSeq: realToSeq, turnsCovered: turns.length };
}

// Persist one invocation turn: append the immutable record, then run the
// compression check. A rejected append (seq collision with a concurrent run)
// is retried once with a fresh probe. Best-effort throughout — history
// persistence must never fail the user-visible turn.
function persistTurn(conv, chatObjId, rec) {
  var res;
  try { res = conv.appendTurn(chatObjId, rec); } catch (e) {
    res = { ok: false, error: (e && e.message) ? e.message : String(e) };
  }
  if (!res.ok) {
    var fresh = -1;
    try { fresh = conv.lastSeq(chatObjId) + 1; } catch (e) {}
    if (fresh >= 0 && fresh !== rec.seq) {
      rec.seq = fresh;
      try { res = conv.appendTurn(chatObjId, rec); } catch (e2) {
        res = { ok: false, error: (e2 && e2.message) ? e2.message : String(e2) };
      }
    }
  }
  if (!res.ok) {
    console.log("[mem] appendTurn failed: " + res.error);
    return res;
  }
  try {
    var comp = maybeCompressTurns(conv, chatObjId, rec.seq);
    if (comp && !comp.skipped) {
      console.log("[mem] compressed turns " + comp.fromSeq + ".." + comp.toSeq +
                  " → chunk #" + comp.chunkSeq + " (" + comp.turnsCovered + " turns)");
    } else if (comp && comp.error) {
      console.log("[mem] compression skipped: " + comp.error);
    }
  } catch (e) {
    console.log("[mem] compression error: " + (e && e.message ? e.message : e));
  }
  return res;
}

// ============================================================================
// Space-Context post-turn splitter
// ============================================================================
// When Main's markdown crosses SPACE_CTX_SPLIT_CHARS we run a one-shot LLM
// pass that rewrites the whole space-context corpus. Input and output share
// the same format — one markdown document where each `# Title` section is one
// context object. After parsing, all existing space_context objects are
// deleted and fresh ones are recreated in order. This trades id-stability for
// parser simplicity; any previously-shared any:// link to a space_context
// object will go stale after a split.

function _parseSpaceContextSections(md) {
  if (!md) return [];
  var lines = md.split(/\r?\n/);
  var sections = [];
  var current = null;
  for (var i = 0; i < lines.length; i++) {
    var line = lines[i];
    var m = /^#\s+(.+?)\s*$/.exec(line);
    if (m) {
      if (current) sections.push(current);
      current = { title: m[1].trim(), content: "" };
    } else if (current) {
      current.content += (current.content ? "\n" : "") + line;
    }
    // Lines before the first header are dropped — the split prompt mandates
    // that output begins with `# Main`, so there should be nothing to drop.
  }
  if (current) sections.push(current);
  for (var j = 0; j < sections.length; j++) {
    sections[j].content = sections[j].content.replace(/^\n+/, "").replace(/\n+$/, "");
  }
  return sections;
}

function _escapeRegex(s) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Rewrite markdown-link references inside Main so they point at the freshly
// created child ids. Handles both `[Title](...anything...)` and bare `[Title]`
// (when the LLM chose the shorter form described in the split prompt).
function _rewriteChildLinks(mainMd, titleToId, spaceId) {
  var out = mainMd;
  for (var title in titleToId) {
    if (!Object.prototype.hasOwnProperty.call(titleToId, title)) continue;
    var id = titleToId[title];
    var esc = _escapeRegex(title);
    var replacement = "[" + title + "](any://" + (spaceId || "_") + "/" + id + ")";
    out = out.replace(new RegExp("\\[" + esc + "\\]\\([^)]*\\)", "g"), replacement);
    out = out.replace(new RegExp("\\[" + esc + "\\](?!\\()", "g"), replacement);
  }
  return out;
}

function maybeSplitSpaceContext(client, breadcrumb) {
  var current = loadSpaceContextMain(client);
  if (!current) {
    if (breadcrumb) breadcrumb("[space-context] split skipped: Main not found — redeploy to recreate it");
    return { skipped: true, reason: "no-main" };
  }
  if (current.markdown.length <= SPACE_CTX_SPLIT_CHARS) {
    return { skipped: true, reason: "under-threshold", size: current.markdown.length };
  }

  var children = getChildSpaceContexts(client);
  // Fetch markdown for every child — the splitter needs the full corpus in one
  // prompt so it can redistribute content freely.
  var childrenFull = [];
  for (var ci = 0; ci < children.length; ci++) {
    var full = client.getObject(children[ci].id, { resolveRefs: false });
    childrenFull.push({ id: children[ci].id, name: children[ci].name, markdown: (full && full.markdown) || "" });
  }

  var corpusParts = ["# Main\n" + current.markdown];
  for (var ci2 = 0; ci2 < childrenFull.length; ci2++) {
    corpusParts.push("# " + childrenFull[ci2].name + "\n" + childrenFull[ci2].markdown);
  }
  var corpus = corpusParts.join("\n\n");

  var prompt =
    "You are reorganizing the space-context corpus of an agent. The corpus is a set of markdown files, one per `# Title` section below. " +
    "Main is injected into every future system prompt; child files are fetched on-demand when the agent thinks a topic matches a title. " +
    "Main has grown past its soft ceiling (~" + SPACE_CTX_SPLIT_CHARS + " chars) and needs to be rebalanced.\n\n" +
    "OUTPUT FORMAT: a single markdown document with the same `# Title` structure. `# Main` MUST be present and MUST come first. Every other `# Title` becomes one child context file. Output nothing outside the sections — no preamble, no trailing commentary.\n\n" +
    "RULES:\n" +
    "- Reorganize, do NOT summarize. Do not drop facts.\n" +
    "- Main should shrink to well under the ceiling (aim ≤ " + SPACE_CTX_TARGET_CHARS + " chars). Rewrite Main as a map: short subheaders + `[Child Title](any://spaceId/objectId)` links. You can use the plain form `[Child Title]` if easier — the runtime will rewrite it to a real link after creation.\n" +
    "- Use existing titles when the content continues an existing topic; invent new titles freely for genuinely new groupings. No expectation of id continuity — the corpus is rewritten wholesale.\n" +
    "- Preserve decision/flow history at a high level: when a rule has changed, keep both forms (\"previously X; now Y\"). Do NOT preserve history for trivial enumerations (field lists, property renames).\n" +
    "- Keep distinct parts distinct — never merge two child files into one unless the underlying topics truly unified.\n\n" +
    "CORPUS:\n\n" + corpus;

  var respText = null;
  try {
    var resp = llm.chat([{ role: "user", content: prompt }], {});
    if (resp && resp.content) {
      var parts = [];
      for (var pi = 0; pi < resp.content.length; pi++) {
        if (resp.content[pi].type === "text") parts.push(resp.content[pi].text);
      }
      respText = parts.join("\n").trim();
    }
  } catch (e) {
    if (breadcrumb) breadcrumb("[space-context] split LLM failed: " + (e && e.message ? e.message : String(e)));
    return { skipped: true, reason: "llm-failed" };
  }
  if (!respText) {
    if (breadcrumb) breadcrumb("[space-context] split skipped: empty LLM response");
    return { skipped: true, reason: "empty-response" };
  }

  var sections = _parseSpaceContextSections(respText);
  if (sections.length === 0 || sections[0].title !== "Main") {
    if (breadcrumb) breadcrumb("[space-context] split aborted: response missing `# Main` as first section");
    return { skipped: true, reason: "no-main-section" };
  }
  var seenTitles = {};
  for (var si = 0; si < sections.length; si++) {
    var t = sections[si].title;
    if (!t) { if (breadcrumb) breadcrumb("[space-context] split aborted: empty title"); return { skipped: true, reason: "empty-title" }; }
    if (seenTitles[t]) { if (breadcrumb) breadcrumb("[space-context] split aborted: duplicate title " + t); return { skipped: true, reason: "duplicate-title" }; }
    seenTitles[t] = true;
  }

  // Delete every existing space_context object (Main + children) and recreate
  // from the parsed sections. Best-effort — a per-id delete failure is logged
  // but doesn't abort the whole operation.
  var existingIds = [current.id];
  for (var xi = 0; xi < childrenFull.length; xi++) existingIds.push(childrenFull[xi].id);
  for (var di = 0; di < existingIds.length; di++) {
    try { client.deleteObject(existingIds[di]); } catch (e) {}
  }

  var mainSection = sections[0];
  var childSections = sections.slice(1);

  var titleToNewId = {};
  var createdChildren = 0;
  for (var ki = 0; ki < childSections.length; ki++) {
    var cs = childSections[ki];
    var r = client.createObject("space_context", { name: cs.title, body: cs.content });
    if (r && r.ok) {
      titleToNewId[cs.title] = r.object.id;
      createdChildren++;
    } else if (breadcrumb) {
      breadcrumb("[space-context] failed to create child '" + cs.title + "': " + (r && r.error));
    }
  }

  var finalMainMd = _rewriteChildLinks(mainSection.content, titleToNewId, client.config.spaceId);
  var mainRes = client.createObject("space_context", { name: "Main", body: finalMainMd });
  if (!mainRes || !mainRes.ok) {
    if (breadcrumb) breadcrumb("[space-context] failed to create new Main: " + (mainRes && mainRes.error));
    return { skipped: false, ok: false, reason: "main-create-failed" };
  }

  if (breadcrumb) {
    breadcrumb("[space-context] split complete: Main=" + finalMainMd.length + " chars, " + createdChildren + " child file" + (createdChildren === 1 ? "" : "s"));
  }
  return { skipped: false, ok: true, mainId: mainRes.object.id, children: createdChildren };
}

// Format an ISO timestamp as "[Wkd YYYY-MM-DD HH:MM UTC]" so each rendered
// chat message carries the date+time+weekday it happened. Returns "" for
// unparseable input — the prefix is dropped silently when ts is missing.
export function _formatHistoryTs(isoTs) {
  if (!isoTs) return "";
  var d;
  try { d = new Date(isoTs); } catch (e) { return ""; }
  if (!d || isNaN(d.getTime())) return "";
  var weekdays = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
  var pad = function(n) { return n < 10 ? "0" + n : String(n); };
  return weekdays[d.getUTCDay()] + " " +
         d.getUTCFullYear() + "-" + pad(d.getUTCMonth() + 1) + "-" + pad(d.getUTCDate()) +
         " " + pad(d.getUTCHours()) + ":" + pad(d.getUTCMinutes()) + " UTC";
}

// Render prior turn RECORDS (structured agent_turns rows, oldest-first) into
// an ordered list of alternating {role, content} messages. Each turn
// becomes:
//   { role: "user", content: "[ts] user-ask\n[effects: ...]" }
//   { role: "assistant", content: "[think]\n...\n[/think]\n\nassistant-final-text" }
// The model sees its own past chain (including intermediate reasoning between
// tool_use blocks) without having to re-see the raw cell code, which adds no
// value in follow-ups.
//
// `think` lives on the assistant side as a `[think]…[/think]` prefix block:
// it IS the model's own prior reasoning, so semantically it belongs there.
// External readers (transcript dumps, debug logs) no longer see user
// messages that "speak" in the model's voice.
//
// Only the user message gets a `[Wkd YYYY-MM-DD HH:MM UTC]\n` prefix — that
// header is metadata about when the user spoke, not something the assistant
// authored. Putting it on assistant messages too caused the model to mimic
// the pattern and emit fake `[ts]\n` prefixes in its own outputs (which then
// leaked into chatReply).
export function renderTurnMessages(turns) {
  var msgs = [];
  for (var i = 0; i < turns.length; i++) {
    var t = turns[i];
    var ts = _formatHistoryTs(_unixToIso(t.createdAt));
    var tsPrefix = ts ? "[" + ts + "]\n" : "";

    var userBlocks = [];
    if (t.userText) {
      var userLine = t.userName ? "(@" + t.userName + ") " + t.userText : t.userText;
      userBlocks.push(tsPrefix + userLine);
    }
    if (t.effects && t.effects.length > 0) {
      userBlocks.push("[effects]\n" + t.effects.join("\n"));
    }
    if (userBlocks.length > 0) {
      msgs.push({ role: "user", content: userBlocks.join("\n\n") });
    }

    var asstBlocks = [];
    if (t.think) asstBlocks.push("[think]\n" + t.think + "\n[/think]");
    // replies = the discrete chat bubbles (intermediates + final). Join into
    // one assistant message to preserve Anthropic's user/assistant
    // alternation. Strip any `[Wkd …]` prefix the model may have mimicked.
    var replies = t.replies || [];
    var cleanedAsst = [];
    for (var ai = 0; ai < replies.length; ai++) {
      var cleaned = _stripTsPrefix(replies[ai]);
      if (cleaned) cleanedAsst.push(cleaned);
    }
    if (cleanedAsst.length > 0) asstBlocks.push(cleanedAsst.join("\n\n"));

    if (asstBlocks.length > 0) {
      msgs.push({ role: "assistant", content: asstBlocks.join("\n\n") });
    } else if (userBlocks.length > 0) {
      // No assistant text recorded — synthesize a minimal placeholder so the
      // Anthropic alternation invariant holds.
      msgs.push({ role: "assistant", content: "(no final text recorded)" });
    }
  }
  return msgs;
}

// Load an agent-skill object by its `Agent Skill.agent_skill_name`. Returns
// "" when no such skill exists so the caller can concatenate freely.
//
// Cross-space: tries the user space first, then falls back to the system
// (private) space. Lets users override a system skill (e.g. _any) by
// deploying a skill with the same name in their own space — user wins.
// When the client has no systemSpaceId, the "system" pass is a no-op via
// anyHelper's _pathForScope (returns the user spacePath).
function _loadSkillMarkdown(client, skillName) {
  var scopes = ["user", "system"];
  for (var s = 0; s < scopes.length; s++) {
    var scope = scopes[s];
    var objects;
    try {
      objects = client.getObjects("agent_skill", { space: scope });
    } catch (e) {
      continue;
    }
    if (!objects || objects.length === 0) continue;
    for (var i = 0; i < objects.length; i++) {
      var o = objects[i];
      if (!o || getProp(o, "agent_skill.agent_skill_name") !== skillName) continue;
      try {
        var full = client.getObject(o.id, { space: scope });
        if (full && full.markdown) return full.markdown;
      } catch (e2) {}
      return "";
    }
  }
  return "";
}

function _loadSoulMarkdown(client) { return _loadSkillMarkdown(client, "_soul"); }
function _loadAnySkill(client) { return _loadSkillMarkdown(client, "_any"); }
function _loadToolcallerSkill(client) { return _loadSkillMarkdown(client, "_toolcaller"); }
function _loadSpaceContextSkill(client) { return _loadSkillMarkdown(client, "_space_context"); }
function _loadMetaSkillMarkdown(client) { return _loadSkillMarkdown(client, "_meta_skill"); }

// Render the "Space Context" section for the system prompt. skillMd is the
// static `_space_context.md` guidance; mainObj = { id, markdown }; children is
// [{id, name}]. Returns "" when skillMd is empty (feature gated off).
function _loadSpaceContextSection(skillMd, mainObj, children, spaceId) {
  if (!skillMd) return "";
  var sid = spaceId || "_";
  var parts = ["\n\n---\n\n", skillMd, "\n\n### Main Space Context\n\n"];
  if (mainObj && mainObj.id) {
    parts.push("[Main](any://" + sid + "/" + mainObj.id + ")\n\n");
    parts.push(mainObj.markdown || "_(empty — edit Main to start building the space context)_");
    parts.push("\n");
  } else {
    parts.push("_(Main is unavailable; edits disabled for this turn)_\n");
  }
  if (children && children.length > 0) {
    parts.push("\n### Child Context Files\n\n");
    parts.push("If the current topic matches one of these titles, fetch its content via `anyHelper.getObject(<id>)`.\n\n");
    for (var i = 0; i < children.length; i++) {
      var c = children[i];
      parts.push("- [" + c.name + "](any://" + sid + "/" + c.id + ")\n");
    }
  }
  return parts.join("");
}

// Build the "Available User Skills" section of the system prompt: a short list
// of every non-system agent-skill object deployed in this space, so the agent
// can decide to fetch a skill's full content when the current topic matches.
// The static guidance (what skills are, how to create/update them) lives in
// the `_meta_skill` agent-skill — its markdown is prepended to the list when
// deployed. System skills (synced from cmd/bobrik-watch/skills/ by the
// bobrik-watch bootstrap) are named with a leading underscore (_toolcaller, _soul, …) and are
// excluded — their content is already woven into the system prompt by dedicated
// loaders, so listing them here would be redundant. (We key on the `_` name
// prefix, not a tag: tags are array properties now and these objects don't
// carry one.)
function _loadUserSkillsSection(client) {
  var objects;
  try {
    objects = client.getObjects("agent_skill");
  } catch (e) {
    return "";
  }
  if (!objects || objects.length === 0) return "";
  var lines = [];
  for (var i = 0; i < objects.length; i++) {
    var o = objects[i];
    if (!o) continue;
    var skillName = getProp(o, "agent_skill.agent_skill_name") || "";
    if (skillName.charAt(0) === "_") continue; // system skill — woven in elsewhere
    var title = o.name || skillName || "(untitled skill)";
    var desc = (o.description || "").trim();
    var line = "- [" + title + "](any://" + (client.config.spaceId || "_") + "/" + o.id + ")";
    if (desc) line += " — " + desc;
    lines.push(line);
  }
  // Cold-start: always render _meta_skill guidance when it's deployed, even
  // if the user has no non-system skills yet. Otherwise the agent never
  // learns it can author them — the very case the guidance is meant to
  // teach. Falls back to "" when neither the skill nor the list exists.
  var metaMd = _loadMetaSkillMarkdown(client);
  if (lines.length === 0) {
    return metaMd ? "\n\n" + metaMd + "\n" : "";
  }
  if (metaMd) {
    return "\n\n" + metaMd + "\n\n" + lines.join("\n") + "\n";
  }
  return "\n\n## Available User Skills\n\n" + lines.join("\n") + "\n";
}

// Build the turn-0 "earlier context" user message from compressed chunk
// RECORDS (structured agent_chunks rows, oldest-first). Each line carries the
// drill-down handle — `[chunk #seq, turns fromSeq..toSeq]` — so the model
// knows it can expand any summary back to the exact raw turns via
// `convmemory.expandChunk(seq)`. Returns null if no chunks.
export function renderChunksMessage(chunks) {
  if (!chunks || chunks.length === 0) return null;
  var lines = ["[Earlier context, compressed — expand any entry to its raw turns via convmemory.expandChunk(<chunk #>)]"];
  for (var i = 0; i < chunks.length; i++) {
    var c = chunks[i];
    var ps = _unixToIso(c.periodStart).substring(0, 16);
    var pe = _unixToIso(c.periodEnd).substring(0, 16);
    var body = (c.summary || "").replace(/\n+/g, " ").trim();
    lines.push("— " + ps + ".." + pe + " — " + body +
      " [chunk #" + c.seq + ", turns " + c.fromSeq + ".." + c.toSeq + "]");
  }
  lines.push("[End of earlier context]");
  return { role: "user", content: lines.join("\n") };
}

// ============================================================================
// Debug collector — captures every LLM call as structured records on ONE
// `agent_debug_log` object
// ============================================================================
// One object per invocation, named after the user prompt. Instead of appending
// markdown editor blocks, the collector writes an ordered ARRAY of structured
// records into the object's `agent_debug_log` dataset — one record per log
// entry — via anyHelper.setRecord. Each record carries a monotonic `seq` and a
// `kind` ("boot" | "system_prompt" | "turn" | "done"); readers sort by `seq`
// (or by the zero-padded record id, which matches insertion order) to replay
// the timeline. The dataset is registered server-side by the built-in
// `agent_debug_log` type (internal/agentdebug); DefaultHandler stores raw
// values, so nested arrays/objects (cells, response) round-trip as-is. All
// writes are best-effort: if the agent space lacks the type or a write
// fails, the agent run continues unaffected.

var DC_DATASET = "agent_debug_log";

var _dc = {
  client: null,
  pageId: null,
  spaceId: "",
  userText: "",
  startMs: 0,
  model: "",
  turnCount: 0,
  seq: 0,
  totalIn: 0,
  totalOut: 0,
  totalCost: 0
};

function _dcReset() {
  _dc.client = null; _dc.pageId = null; _dc.spaceId = ""; _dc.userText = "";
  _dc.startMs = 0; _dc.model = ""; _dc.turnCount = 0; _dc.seq = 0;
  _dc.totalIn = 0; _dc.totalOut = 0; _dc.totalCost = 0;
}

// dcDebugLink composes the agent.debugLink drill-down chatReply attaches
// to every reply: any://<spaceId>/<debugPageId>[#turn_<n>] — the UI
// resolves the fragment to the matching turn card on the debug page.
// Empty string when the debug page failed to create (the host omits the
// field then); pass turnN 0 for pre-turn replies (ack, boot failures).
function dcDebugLink(turnN) {
  if (!_dc.pageId || !_dc.spaceId) return "";
  var link = "any://" + _dc.spaceId + "/" + _dc.pageId;
  if (turnN) link += "#turn_" + turnN;
  return link;
}

// Zero-pad a sequence number so lexical record-id order matches insertion order.
function _dcPad(n) {
  var s = "" + n;
  while (s.length < 6) s = "0" + s;
  return s;
}

// Write one structured entry record to the agent_debug_log dataset. Stamps a
// monotonic `seq`, the `kind`, and an ISO `ts`, then merges the caller's
// fields. Best-effort — a missing page/client or a setRecord failure is
// swallowed so the agent run is never affected by debug logging.
function _dcWriteEntry(kind, fields) {
  if (!_dc.client || !_dc.pageId) return;
  var seq = _dc.seq++;
  var rec = { seq: seq, kind: kind };
  try { rec.ts = new Date().toISOString(); } catch (e) {}
  if (fields) {
    for (var k in fields) {
      if (Object.prototype.hasOwnProperty.call(fields, k)) rec[k] = fields[k];
    }
  }
  var recordId = _dcPad(seq) + "_" + kind;
  try { _dc.client.setRecord(_dc.pageId, DC_DATASET, recordId, rec); } catch (e) {}
}

function dcInit(client, spaceId, userText, bootMeta) {
  _dcReset();
  _dc.client = client;
  _dc.spaceId = spaceId || "";
  _dc.userText = userText || "";
  _dc.startMs = Date.now();
  var name = _dc.userText || "(no prompt)";

  // Create the log object (type agent_debug_log) with NO markdown body — the
  // dataset is the content. File it under the host-provided Debug nav folder
  // (best-effort; failure leaves it at root but the run continues).
  try {
    var r = client.createObject("agent_debug_log", { name: name });
    if (r && r.ok && r.object) {
      _dc.pageId = r.object.id;
      var debugFolderId = client.config && client.config.debugFolderId;
      if (debugFolderId) {
        try { client.addToCollection(debugFolderId, _dc.pageId); } catch (e2) {}
      }
    }
  } catch (e) {}

  // First record: boot context (prompt + middleware meta + redacted rawArgs).
  var boot = {
    prompt: _dc.userText || "(no prompt)",
    build: "chat-scoping@v2"
  };
  if (bootMeta) {
    boot.spaceType   = (bootMeta.spaceType === null || bootMeta.spaceType === undefined) ? null : bootMeta.spaceType;
    boot.chatId      = bootMeta.chatId || "";
    boot.identity    = bootMeta.identity || "";
    boot.botIdentity = bootMeta.botIdentity || "";
    if (bootMeta.rawArgs !== undefined) {
      // Redact anything that looks like a secret before it lands on the page.
      var redacted = {};
      try {
        for (var k in bootMeta.rawArgs) {
          if (!Object.prototype.hasOwnProperty.call(bootMeta.rawArgs, k)) continue;
          if (/apiKey|api_key|token|secret|password/i.test(k)) {
            redacted[k] = "(redacted)";
          } else {
            redacted[k] = bootMeta.rawArgs[k];
          }
        }
      } catch (e3) {}
      boot.rawArgs = redacted;
    }
  }
  _dcWriteEntry("boot", boot);
}

// Stringify a tool_result block's content. The block can hold either a plain
// string or an array of {type:"text", text:"..."} parts. Returns the joined
// text plus an "[ERROR]" prefix when the block is marked is_error.
function _formatToolResultContent(block) {
  if (!block) return "";
  var content = block.content;
  var text;
  if (typeof content === "string") {
    text = content;
  } else if (content && content.length) {
    var parts = [];
    for (var i = 0; i < content.length; i++) {
      var c = content[i];
      if (c && c.type === "text" && c.text) parts.push(c.text);
      else if (typeof c === "string") parts.push(c);
    }
    text = parts.join("\n");
  } else {
    text = String(content == null ? "" : content);
  }
  return block.is_error ? "[ERROR] " + text : text;
}

// Record the full system prompt the LLM sees on every turn. Per-turn raw
// responses are captured in dcLogTurn; this fills the only otherwise-
// invisible channel, the `system:` parameter. One `system_prompt` record.
function dcLogInitialContext(systemText) {
  _dcWriteEntry("system_prompt", {
    chars: (systemText || "").length,
    text: systemText || ""
  });
}

function dcLogTurn(opts) {
  if (!_dc.client || !_dc.pageId) return;
  var n = opts.n;
  var resp = opts.resp || {};
  var durationMs = opts.durationMs || 0;
  var toolResults = opts.toolResults || [];
  if (resp.model && !_dc.model) _dc.model = resp.model;

  var content = resp.content || [];
  var resultsById = {};
  for (var ri = 0; ri < toolResults.length; ri++) {
    if (toolResults[ri] && toolResults[ri].tool_use_id) {
      resultsById[toolResults[ri].tool_use_id] = toolResults[ri];
    }
  }

  // One structured cell per tool_use block, paired with its result text and
  // an `executed` flag (false when the turn ended before the cell ran).
  var cells = [];
  for (var i = 0; i < content.length; i++) {
    if (!content[i] || content[i].type !== "tool_use") continue;
    var code = (content[i].input && content[i].input.code) || "";
    var matched = resultsById[content[i].id];
    cells.push({
      code: code,
      result: matched ? _formatToolResultContent(matched) : "",
      isError: matched ? !!matched.is_error : false,
      executed: !!matched
    });
  }

  // Running totals (used by the dcFlush summary record).
  _dc.turnCount = n;
  var inT = 0, outT = 0, cost = null;
  if (resp.usage) {
    var u = resp.usage;
    inT = u.prompt_tokens || u.input_tokens || 0;
    outT = u.completion_tokens || u.output_tokens || 0;
    _dc.totalIn += inT;
    _dc.totalOut += outT;
    if (u.cost !== undefined) { _dc.totalCost += (u.cost || 0); cost = u.cost; }
  }

  // Turn record holds the extracted scalars + the per-cell code/result, plus
  // the raw API `response` — one message object (NOT the window), small, and
  // the only home for the per-turn assistant narration text blocks, the cache
  // counters (cache_read_input_tokens & co — the cache-miss-regression
  // signal), and tool_use ids. The full `messages[]` window stays intentionally
  // NOT stored — it repeats the whole conversation every turn (quadratic
  // growth) and is reconstructible from chat history + prior turn records;
  // only the literal window construction is lost, which we accept.
  _dcWriteEntry("turn", {
    n: n,
    stopReason: resp.stop_reason || "",
    durationMs: durationMs,
    inTokens: inT,
    outTokens: outT,
    cost: cost,
    response: resp,
    cells: cells
  });
}

function dcFlush(opts) {
  opts = opts || {};
  _dcWriteEntry("done", {
    status: opts.status || "?",
    model: _dc.model || "",
    turns: _dc.turnCount,
    totalMs: opts.totalMs || (Date.now() - _dc.startMs),
    totalIn: _dc.totalIn,
    totalOut: _dc.totalOut,
    totalCost: _dc.totalCost,
    finalText: opts.finalText || ""
  });
}

// ============================================================================
// MAIN LOOP
// ============================================================================

function _findToolUseBlocks(content) {
  var blocks = [];
  if (!content || !content.length) return blocks;
  for (var i = 0; i < content.length; i++) {
    if (content[i].type === "tool_use") blocks.push(content[i]);
  }
  return blocks;
}

// Strip a leading `[Wkd YYYY-MM-DD HH:MM UTC]\n` header that earlier model
// runs sometimes echoed from the rendered chat history (the wrapper has
// since been removed for assistant messages, but stored history may still
// contain text the model wrote with the prefix mimicked into its own output).
// Applied both when extracting current model output (prevent re-storing it)
// and when rendering prior assistant entries from history (cleanse old data).
var _TS_PREFIX_RE = /^\[(?:Sun|Mon|Tue|Wed|Thu|Fri|Sat) \d{4}-\d{2}-\d{2} \d{2}:\d{2} UTC\]\s*\n?/;
function _stripTsPrefix(text) {
  if (!text) return text;
  return text.replace(_TS_PREFIX_RE, "");
}

function _findTextBlocks(content) {
  var texts = [];
  if (!content || !content.length) return texts;
  for (var i = 0; i < content.length; i++) {
    if (content[i].type === "text") texts.push(_stripTsPrefix(content[i].text));
  }
  return texts;
}

// Format the result of a run_cell execution into the string content of a
// tool_result block. Up to three sections:
//   - Output: the cell's console.log values, in call order — the model's
//             primary channel. Each inline (full, displayValue) when it fits
//             MAX_VALUE_INLINE_CHARS, else a size+schema stub → logs.get(id, i).
//   - Last value: the cell's final expression, same inline-or-stub rule
//                 (logs.get(id, "last")).
//   - Side Effects: grouped signature counts of every OTHER traced call
//                   (console.log excluded), full one-liner trace stashed under
//                   toolEffects.get(id). Never dumps the full trace inline.
// Structured values for the stubs live in the kernel _valueStore (logs.get);
// the full effects trace lives in _toolEffectsStore (toolEffects.get).

// Trace keys hidden from the LLM entirely — pure harness plumbing. The model
// calls inferSchema() inside cells for ad-hoc shape inspection, and those
// calls get auto-traced by the kernel; we filter them so they don't echo back
// as "Effects".
var HIDDEN_TRACE_KEYS = ["inferSchema"];

// Return a trace map filtered for LLM consumption. First drop harness-internal
// keys (HIDDEN_TRACE_KEYS), then delegate to the kernel-side __prepareToolTraces
// which chains each tool's own __prepareTraces hook. Tool-specific URL/host
// filtering lives in the tool module, not here — see
// feedback_tool_self_contained_filters in the project memory.
function _prepareTracesForLLM(traces) {
  if (!traces) return {};
  var filtered = {};
  for (var k in traces) {
    if (!traces.hasOwnProperty(k)) continue;
    if (HIDDEN_TRACE_KEYS.indexOf(k) !== -1) continue;
    filtered[k] = traces[k];
  }
  if (Object.keys(filtered).length === 0) return {};
  try {
    var res = js.eval(
      "__prepareToolTraces(JSON.parse(args.__tracesJson))",
      { __tracesJson: JSON.stringify(filtered) },
      { persistent: true }
    );
    if (res && !res.error && res.lastValue && typeof res.lastValue === "object") {
      return res.lastValue;
    }
  } catch (e) {}
  return filtered;
}

// Push a cell's full trace one-liner array into the kernel's per-toolUseId
// effect store so the next cell can fetch it via toolEffects.get(toolUseId).
// Best-effort: a stash failure just means the agent loses access to the full
// trace, which is acceptable since the summary header is already in-context.
function _stashEffectsToKernel(toolUseId, traceLines) {
  if (!toolUseId || !traceLines || traceLines.length === 0) return;
  try {
    js.eval(
      "globalThis._toolEffectsStore[args.id] = JSON.parse(args.tracesJson);",
      { id: toolUseId, tracesJson: JSON.stringify(traceLines) },
      { persistent: true }
    );
  } catch (e) {}
}

// Stash a cell's structured values (console.log values in call order + the Last
// value) into the kernel's per-toolUseId value store so the next cell can fetch
// them via logs.get(id, i) / logs.get(id, "last"). Always called, so indices in
// the Output section stay aligned with logs.get even for inlined values.
// Best-effort: a stash failure just means a stubbed value can't be walked.
function _stashValuesToKernel(toolUseId, logValues, hasLast, lastValue) {
  if (!toolUseId) return;
  try {
    js.eval(
      "globalThis._valueStore[args.id] = { logs: JSON.parse(args.logsJson)," +
        " last: args.hasLast ? JSON.parse(args.lastJson) : undefined };",
      {
        id: toolUseId,
        logsJson: JSON.stringify(logValues || []),
        hasLast: !!hasLast,
        lastJson: JSON.stringify(hasLast ? lastValue : null)
      },
      { persistent: true }
    );
  } catch (e) {}
}

// Pull the structured console.log values (in call order) out of a prepared
// trace map. The runtime records each log's structured arg(s) under the "value"
// output key (single arg → the value, multi → array); the "log" string form is
// the fallback for an older runtime without "value".
function _consoleValues(traces) {
  var rec = traces && traces["console.log"];
  if (!rec) return [];
  if (rec["value"] && rec["value"].length) return rec["value"].slice();
  if (rec["log"] && rec["log"].length) return rec["log"].slice();
  return [];
}

// Render one structured value inline (full, via displayValue) when it fits the
// per-value budget, else a size+schema stub pointing the model at logs.get for
// the real value. `selector` is the rendered second arg to logs.get — a numeric
// index for a log, or the literal "last" for the Last value.
function _renderValueOrStub(value, toolUseId, selector) {
  var rendered = displayValue(value);
  if (rendered.length <= MAX_VALUE_INLINE_CHARS) return rendered;
  return "[" + rendered.length + " chars, schema " + inferSchema(value) +
    " — logs.get(\"" + toolUseId + "\", " + selector + ") to walk]";
}

// Build the "Output:" section from the cell's console.log values. Each entry is
// numbered so a stub's logs.get(id, N) maps unambiguously back to its line.
function _buildOutputSection(logValues, toolUseId) {
  if (!logValues || logValues.length === 0) return "";
  var lines = ["Output:"];
  for (var i = 0; i < logValues.length; i++) {
    lines.push("  #" + i + " " + _renderValueOrStub(logValues[i], toolUseId, String(i)));
  }
  return lines.join("\n");
}

// Build the "Side Effects:" section — every traced call EXCEPT console.log,
// collapsed to grouped signature counts (never the full inline dump), with the
// full one-liner trace stashed under the tool_use_id so the model can always
// pull it via toolEffects.get(id). isError tags the header for the failed path.
function _buildSideEffects(traces, toolUseId, isError) {
  var nonConsole = {};
  var any = false;
  for (var k in traces) {
    if (!traces.hasOwnProperty(k)) continue;
    if (k === "console.log") continue;
    nonConsole[k] = traces[k];
    any = true;
  }
  if (!any) return "";
  var oneLiner = formatTraceOneLiner(nonConsole);
  if (!oneLiner) return "";
  if (toolUseId) _stashEffectsToKernel(toolUseId, oneLiner.split("\n"));
  var summary = summarizeTrace(nonConsole);
  var total = summary ? summary.totalCalls : 0;
  var lines = [(isError ? "Side Effects (before the error)" : "Side Effects") +
    ": " + total + " call" + (total === 1 ? "" : "s")];
  if (summary) {
    for (var i = 0; i < summary.groupLines.length; i++) lines.push(summary.groupLines[i]);
  }
  if (toolUseId) {
    lines.push("  full trace: toolEffects.get(\"" + toolUseId + "\")  // one-liner per call, in order");
  }
  return lines.join("\n");
}

// Build the tool_result content: Output (the model's console.log values) →
// Last value → Side Effects (a summary of everything else). Console output is
// the primary channel; big values in Output/Last value collapse to a
// size+schema stub walkable via logs.get. Reused for both success and error
// (errContent prepended by the caller on the error path).
function _buildResultDigest(traces, lastValue, hasLast, toolUseId, isError) {
  var logValues = _consoleValues(traces);
  // Always stash so logs.get(id, i)/("last") works for any stubbed value and
  // indices stay aligned with the Output numbering.
  _stashValuesToKernel(toolUseId, logValues, hasLast, hasLast ? lastValue : null);

  var parts = [];
  var output = _buildOutputSection(logValues, toolUseId);
  if (output) parts.push(output);
  if (hasLast) {
    parts.push("Last value: " + _renderValueOrStub(lastValue, toolUseId, "\"last\""));
  }
  var side = _buildSideEffects(traces, toolUseId, isError);
  if (side) parts.push(side);
  return parts;
}

function formatToolResult(result, toolUseId) {
  if (!result) return "(no result)";
  // result.callTrace — per-call trace scoped to THIS eval invocation (includes
  // mock-hit calls, unlike traceDiff). result.traces is the cumulative session
  // log and would leak prior cells' effects into this cell's Side Effects.
  // See docs/runtime-task-per-call-traces.md.
  var traces = _prepareTracesForLLM(result.callTrace || {});
  var hasLast = result.lastValue !== undefined && result.lastValue !== null;
  var parts = _buildResultDigest(traces, result.lastValue, hasLast, toolUseId, false);
  if (parts.length === 0) parts.push("(cell completed, no output, no return value, no effects)");
  return parts.join("\n\n");
}

// Execute one tool_use block. Returns {block, effects}.
// Errors flow back to the model via is_error: true — no tracer, the model
// recovers itself in the next turn from the error message + partial trace.
function executeToolUse(block, args) {
  if (block.name !== "run_cell") {
    return {
      block: {
        type: "tool_result",
        tool_use_id: block.id,
        content: "Unknown tool: " + block.name + ". The only available tool is run_cell.",
        is_error: true
      }
    };
  }

  var code = block.input && block.input.code;
  if (!code || typeof code !== "string" || code.trim() === "") {
    return {
      block: {
        type: "tool_result",
        tool_use_id: block.id,
        content: "Empty code in run_cell input.",
        is_error: true
      }
    };
  }

  var result = js.eval(code, args, { persistent: true, enableWrapMethods: true });

  // Extract effects digest (structured one-liners) from this cell's trace so
  // the caller can accumulate them across the whole invocation for persistence.
  var cellEffects = extractEffects(result && result.callTrace, args && args.spaceId);

  // On any error, return is_error: true with the error message + any output /
  // partial side-effects from before the error fired (a failed cell that
  // console.log'd before throwing still surfaces those logs, walkable via
  // logs.get; lastValue is absent on the error path).
  if (result && result.error) {
    var errContent = "Error: " + result.error;
    if (result.callTrace && Object.keys(result.callTrace).length > 0) {
      var partialFiltered = _prepareTracesForLLM(result.callTrace);
      var partialParts = _buildResultDigest(partialFiltered, null, false, block.id, true);
      if (partialParts.length > 0) errContent += "\n\n" + partialParts.join("\n\n");
    }
    return {
      block: {
        type: "tool_result",
        tool_use_id: block.id,
        content: errContent,
        is_error: true
      },
      effects: cellEffects
    };
  }

  return {
    block: {
      type: "tool_result",
      tool_use_id: block.id,
      content: formatToolResult(result, block.id)
    },
    effects: cellEffects
  };
}

export function main(args) {
  if (!args) args = {};
  if (!args.apiBaseUrl) args.apiBaseUrl = env.ANYTYPE_API_URL;
  if (!args.apiKey)     args.apiKey     = env.ANYTYPE_API_KEY;
  if (!args.spaceId)    args.spaceId    = env.ANYTYPE_SPACE_ID;
  // No text (e.g. image-only message, system event) — nothing to reply to.
  // Silent no-op so the host doesn't surface a spurious error in chat.
  if (!args.text || !String(args.text).trim()) return "";

  // Middleware-supplied context. spaceType: 0 Unknown, 1 Regular, 2 Tech,
  // 3 Chat, 4 OneToOne. Ignore Unknown/Tech — the bot only operates in
  // Regular/Chat/OneToOne spaces.
  //
  // Coerce numeric strings — some hosts round-trip JSON args that turn
  // `3` into `"3"`. Without this, a misformatted arg silently collapses
  // to the legacy "no-spaceType → respond + shared history" path.
  var spaceType = null;
  if (typeof args.spaceType === "number") spaceType = args.spaceType;
  else if (typeof args.spaceType === "string" && /^\d+$/.test(args.spaceType)) {
    spaceType = parseInt(args.spaceType, 10);
  }
  var currentChatId  = args.chatId || null;
  var botIdentity    = args.botIdentity || env.ANYTYPE_BOT_IDENTITY || null;
  var senderIdentity = args.identity || null;

  // Surface middleware-supplied args to the trace so a bot stuck in
  // legacy shared-history mode can be diagnosed from the next message.
  // Logged before the ignore gate so Unknown/Tech short-circuits are
  // still visible in traces.
  console.log("[boot] middleware args: spaceType=" + spaceType +
              " chatId=" + (currentChatId || "(none)") +
              " identity=" + (senderIdentity || "(none)") +
              " botIdentity=" + (botIdentity || "(none)"));

  // Sub-agent / quiet mode. Triggered by `args.__quiet === true` from a
  // wrapping caller (typically `subagent` invoking via runProgram). When
  // set we:
  //   - capture every chatReply into a local buffer instead of posting,
  //   - skip chat-history load AND persist (sub-agent runs are isolated),
  //   - bypass the spaceType filter and the @-mention/bot-channel gate
  //     (the wrapper is the only authority — middleware gates don't apply),
  //   - return the joined captured text from main() so the caller sees the
  //     reply in `runProgram(...).result` instead of an empty string.
  // Everything else (tool discovery, dcInit/dcFlush debug records, LLM loop)
  // runs unchanged so a sub-agent debug page still survives the call.
  // Accept boolean true (JSON / runProgram path) or string "true" (CLI path).
  var __quiet = args.__quiet === true || args.__quiet === "true";
  var __captured = [];
  var chatReply = __quiet
    ? function(payload) {
        var t = payload && typeof payload === "object" && payload.text ? payload.text : payload;
        var s = String(t);
        if (s.indexOf("✅ ") === 0) s = s.substring(2);
        __captured.push(s);
        return { id: "quiet" };
      }
    : globalThis.chatReply;

  // Wrap the rest of main() in an IIFE so a single post-process at the
  // bottom can convert any return path into the captured-text answer
  // when __quiet is set. Existing returns inside the body land in
  // _coreResult unchanged.
  var _coreResult = (function _runCore() {

  if (!__quiet && spaceType !== null && spaceType !== 1 && spaceType !== 3 && spaceType !== 4) {
    return "";
  }

  var invocationStart = new Date().toISOString();

  // Host-side scratch client used only inside main() for boot. NEVER reaches
  // the kernel — the kernel sees `anyHelper` (built from a fresh instance
  // inside the boot prelude). Named bootClient to make this distinction loud.
  var bootClient = createClient({
    apiBaseUrl: args.apiBaseUrl,
    apiKey: args.apiKey,
    spaceId: args.spaceId,
    debugFolderId: args.debugFolderId
  });

  // Mention gate: in Regular/Chat spaces, only respond in `bot-*` channels or
  // when the bot is @mentioned. In OneToOne (spaceType 4) we always respond.
  // Gate runs BEFORE tool discovery + debug-object creation so silent turns
  // in noisy group chats leave no side effects on the space. The gate only
  // needs bootClient to hit /members and /objects, nothing from the kernel.
  // In __quiet (sub-agent) mode there's no middleware to gate against —
  // the wrapping caller is authoritative. Skip the gate and synthesise
  // a minimal `gate` so downstream botName resolution still works.
  var gate = __quiet
    ? { respond: true, chatName: null, botHandle: null }
    : shouldRespond(bootClient, {
        spaceType: spaceType,
        chatId: currentChatId,
        botIdentity: botIdentity,
        text: args.text
      });
  if (!gate.respond) {
    return "";
  }

  var tools = bootClient.getTools();
  if (!tools || tools.length === 0) {
    return "Error: no tools available in space";
  }

  var toolDocs;
  try {
    toolDocs = _buildToolDocs(bootClient, tools);
  } catch (e) {
    return "Boot failed: " + (e && e.message ? e.message : String(e));
  }

  // Begin debug-collection AFTER toolDocs is built (we know boot is salvageable)
  // but BEFORE js.reset(), so dcInit's createObject doesn't get reset.
  dcInit(bootClient, args.spaceId, args.text, {
    spaceType: spaceType,
    chatId: currentChatId,
    identity: senderIdentity,
    botIdentity: botIdentity,
    rawArgs: args
  });

  // No run-start ack message: the UI starts its thinking indicator
  // locally when the user sends a message to the bao chat (see
  // ../any-ui/docs/tasks/agent-thinking-on-send.md); the indicator
  // resolves on the first agent reply (done:true terminal, done:false
  // keeps it going).

  // Resolve sender display name once per invocation so turn records can
  // distinguish users in group chats. Falls back to the raw identity and
  // finally to no-name (pre-scoping behaviour).
  var senderName = null;
  if (senderIdentity) {
    senderName = resolveMemberName(bootClient, args.spaceId, senderIdentity) || senderIdentity;
  }

  // Resolve bot display name too — reuses the gate's result when available
  // (chat name didn't start with `bot-`, so we already fetched it), otherwise
  // looks it up fresh. Written onto each turn record as assistant_name so
  // history reads "assistant (@handle)> …" symmetrically with users.
  var botName = gate.botHandle || null;
  if (!botName && botIdentity) {
    botName = resolveMemberName(bootClient, args.spaceId, botIdentity);
  }

  // Reset and bootstrap the persistent kernel — single js.eval that imports
  // every tool and binds it as a global wrapped via __makeToolFacade.
  js.reset();
  var prelude = _buildBootPrelude(toolDocs);
  var bootResult = js.eval(prelude.code, args, { persistent: true });
  if (bootResult && bootResult.error) {
    var bootMsg = "Bootstrap failed: " + bootResult.error;
    // Terminal reply (not just a return) — the ack already opened a
    // typing indicator that only a done:true message closes.
    chatReply({ text: bootMsg, done: true, debugLink: dcDebugLink(0) });
    dcFlush({ status: "bootstrap_failed", finalText: bootMsg });
    return bootMsg;
  }

  // Build the system prompt from required agent skills deployed in the space.
  // Both `_any` (identity + `any` mechanics) and `_toolcaller` (run_cell
  // semantics + cell patterns) are load-bearing — fail hard with a clear
  // user-visible message if either is missing. No stale in-code fallback:
  // the skills in assistant-skills/*.md are the single source of truth.
  var anySkill = _loadAnySkill(bootClient);
  if (!anySkill) {
    var missA = "System skill `_any` is missing from this space. Re-run the bobrik-watch bootstrap (`bobrik-watch --bootstrap`, or `kill -HUP $(cat .bobrik-pid)`) to deploy agent skills.";
    chatReply({ text: missA, done: true, debugLink: dcDebugLink(0) });
    dcFlush({ status: "skill_missing", finalText: missA });
    return "";
  }
  var toolcallerSkill = _loadToolcallerSkill(bootClient);
  if (!toolcallerSkill) {
    var missT = "System skill `_toolcaller` is missing from this space. Re-run the bobrik-watch bootstrap (`bobrik-watch --bootstrap`, or `kill -HUP $(cat .bobrik-pid)`) to deploy agent skills.";
    chatReply({ text: missT, done: true, debugLink: dcDebugLink(0) });
    dcFlush({ status: "skill_missing", finalText: missT });
    return "";
  }
  // Space Context: gated on the optional `_space_context` agent-skill. When
  // the skill is deployed, bootstrap the Main singleton and render the
  // injected section; otherwise skip entirely. The feature is also used by the
  // post-turn split hook, so keep the skill markdown around to decide then.
  var spaceContextSkill = _loadSpaceContextSkill(bootClient);
  var spaceContextMain = null;
  var spaceContextChildren = [];
  if (spaceContextSkill) {
    spaceContextMain = loadSpaceContextMain(bootClient);
    spaceContextChildren = getChildSpaceContexts(bootClient);
  }

  var fullSystemText =
    anySkill + "\n\n---\n\n" +
    toolcallerSkill +
    _loadUserSkillsSection(bootClient) +
    _loadSpaceContextSection(spaceContextSkill, spaceContextMain, spaceContextChildren, args.spaceId) +
    _buildToolsPromptSection(toolDocs) +
    _fetchCategoriesSection();

  // If the space has a `_soul` agent-skill object, append its markdown as
  // reply-style guidance so the model's final answers take on that voice.
  var soulMd = _loadSoulMarkdown(bootClient);
  if (soulMd) {
    fullSystemText += "\n\n## Your persona, reply style:\n\n" + soulMd + "\n";
  }
  var systemBlocks = [
    { type: "text", text: fullSystemText, cache_control: { type: "ephemeral" } }
  ];

  // Log the system prompt to the debug page — per-turn logs already capture
  // the `messages` array, but the `system:` parameter was previously
  // invisible. Now the debug page opens with the full initial context.
  dcLogInitialContext(fullSystemText);

  // --- Chat history bootstrap ------------------------------------------------
  // Load the boot context from the structured datasets on the chat object:
  // the newest TURNS_TO_INJECT raw turn records plus the newest
  // CHAT_CHUNKS_TO_INJECT compressed chunks — two bounded indexed queries,
  // no anchor walk, no markdown parsing. Scoping is structural: the chat
  // object hosts its own agent_turns/agent_chunks.
  //
  // In __quiet (sub-agent) mode we deliberately skip load AND persist — the
  // sub-agent task is isolated. Without a chatId (legacy caller, no
  // middleware) there is no host object for the turn log, so history is
  // disabled the same way.
  var conv = createConvMemory(bootClient);
  var historyEnabled = !__quiet && !!currentChatId;
  var priorTurns = [];
  var priorChunks = [];
  var nextSeq = 0;
  if (historyEnabled) {
    try {
      priorTurns = conv.recentTurns(currentChatId, TURNS_TO_INJECT);
      nextSeq = priorTurns.length > 0 ? priorTurns[priorTurns.length - 1].seq + 1 : 0;
    } catch (e) {
      console.log("[mem] turn window load failed: " + (e && e.message ? e.message : e));
      historyEnabled = false;
    }
    try {
      priorChunks = conv.recentChunks(currentChatId, CHAT_CHUNKS_TO_INJECT);
    } catch (e) {}
  }

  var messages = [];
  var chunkMsg = renderChunksMessage(priorChunks);
  if (chunkMsg) messages.push(chunkMsg);

  var windowMsgs = renderTurnMessages(priorTurns);
  for (var wmi = 0; wmi < windowMsgs.length; wmi++) messages.push(windowMsgs[wmi]);

  // Current user ask — prefix with the same `[Wkd YYYY-MM-DD HH:MM UTC]`
  // header used for prior turns, so every message the model sees carries
  // its own timestamp.
  var nowTs = _formatHistoryTs(invocationStart);
  var nowPrefix = nowTs ? "[" + nowTs + "]\n" : "";
  var userLineNow = senderName ? "(@" + senderName + ") " + args.text : args.text;

  // Current view pointer — what the user is looking at in the UI as of this
  // message (the `ui-context` object the web UI maintains in this space; see
  // anyHelper.getUIContext). Attached to the user message rather than the
  // cached system prompt because it changes per navigation. "this"/"that
  // page" in the ask usually means this view — pass uiCtx.spaceId as the
  // `space` option to act there. Absent until the UI first reports.
  var uiCtx = null;
  try { uiCtx = bootClient.getUIContext(); } catch (e) {}
  if (uiCtx && uiCtx.spaceId) {
    userLineNow += "\n[user's current view — space: " + uiCtx.spaceId +
      (uiCtx.objectId ? ", object: " + uiCtx.objectId : "") +
      (uiCtx.view ? ", view: " + uiCtx.view : "") + "]";
  }
  messages.push({ role: "user", content: nowPrefix + userLineNow });

  // Per-invocation accumulators (written to history on end_turn).
  var turnThinkParts = [];
  var turnEffects = [];

  // Build the immutable agent_turns record for persistTurn. `replies` is the
  // ordered list of discrete chat bubbles the user saw (intermediates +
  // final); heavy per-LLM-turn detail (cells, raw responses) stays in the
  // debug log, linked via debugRef. Empty optional fields are omitted — the
  // server rejects empty-string values for present optional fields.
  function buildTurnRec(replies, stopReason) {
    var rec = { seq: nextSeq, userText: args.text };
    if (senderName) rec.userName = String(senderName);
    rec.fromAgent = botName ? String(botName) : "bao";
    if (replies && replies.length > 0) rec.replies = replies;
    if (turnEffects.length > 0) rec.effects = turnEffects;
    if (args.msgId) rec.messageIds = [String(args.msgId)];
    if (_dc.pageId) rec.debugRef = _dc.pageId;
    var llmStats = {};
    if (stopReason) llmStats.stopReason = stopReason;
    if (_dc.model) llmStats.model = _dc.model;
    if (_dc.totalIn) llmStats.inTokens = _dc.totalIn;
    if (_dc.totalOut) llmStats.outTokens = _dc.totalOut;
    if (llmStats.stopReason || llmStats.model || llmStats.inTokens || llmStats.outTokens) rec.llm = llmStats;
    return rec;
  }

  for (var iter = 0; ; iter++) {
    var resp;
    var _t = Date.now();
    try {
      resp = llm.chat(messages, {
        system: systemBlocks,
        tools: [RUN_CELL_TOOL]
      });
    } catch (e) {
      chatReply({ text: "LLM error on turn " + (iter + 1) + ": " + (e.message || e), done: true, debugLink: dcDebugLink(iter + 1) });
      var failMsg = "FAILED at turn " + (iter + 1) + ": " + (e.message || e);
      dcFlush({ status: "llm_error", finalText: failMsg });
      return failMsg;
    }
    var _turnMs = Date.now() - _t;

    // max_tokens — output was cut off. We don't retry the cell; instead we
    // close out the turn by asking the model to summarize what's been done so
    // far and emit a final answer (no more tool_use). The summary lands in
    // chat history just like a normal end_turn, so the user sees what
    // happened and the next turn starts clean.
    if (resp.stop_reason === "max_tokens") {
      messages.push({ role: "assistant", content: resp.content });

      // Anthropic requires every tool_use to be paired with a tool_result —
      // synthesize is_error results for any tool_use in the truncated message.
      var maxTokenResults = [];
      var tBlocks = _findToolUseBlocks(resp.content);
      for (var mi = 0; mi < tBlocks.length; mi++) {
        maxTokenResults.push({
          type: "tool_result",
          tool_use_id: tBlocks[mi].id,
          content: "Cell not executed — your response was cut off by max_tokens.",
          is_error: true
        });
      }
      if (maxTokenResults.length > 0) {
        messages.push({ role: "user", content: maxTokenResults });
      }
      messages.push({
        role: "user",
        content: "Your response exceeded max_tokens — the turn is being closed. " +
                 "Stop calling tools. Reply with text only: a brief summary of what was accomplished " +
                 "in this turn, what's still outstanding, and what the user should do next."
      });

      var summaryResp;
      try {
        // Tools omitted on purpose — force a text-only reply.
        summaryResp = llm.chat(messages, { system: systemBlocks });
      } catch (e) {
        var sumErr = "FAILED at turn " + (iter + 1) + ": max_tokens recovery LLM error: " + (e.message || e);
        chatReply({ text: "⚠ max_tokens — recovery summary failed: " + (e.message || e), done: true, debugLink: dcDebugLink(iter + 1) });
        dcLogTurn({ n: iter + 1, resp: resp, durationMs: _turnMs, toolResults: maxTokenResults });
        dcFlush({ status: "max_tokens_summary_failed", finalText: sumErr });
        return sumErr;
      }

      var summaryText = "";
      if (summaryResp && summaryResp.content) {
        summaryText = _findTextBlocks(summaryResp.content).join("\n").trim();
      }
      if (!summaryText) summaryText = "(turn cut off by max_tokens; no summary produced)";
      chatReply({ text: "⚠ max_tokens — turn auto-closed:\n\n" + summaryText, done: true, debugLink: dcDebugLink(iter + 1) });

      // Persist as a normal turn so chat history shows what happened.
      if (historyEnabled) {
        var assistantEntries = [];
        for (var __ti = 0; __ti < turnThinkParts.length; __ti++) {
          if (turnThinkParts[__ti]) assistantEntries.push(turnThinkParts[__ti]);
        }
        assistantEntries.push("[max_tokens auto-summary] " + summaryText);
        persistTurn(conv, currentChatId, buildTurnRec(assistantEntries, "max_tokens"));
      }

      dcLogTurn({ n: iter + 1, resp: resp, durationMs: _turnMs, toolResults: maxTokenResults });
      dcFlush({ status: "max_tokens_summary", finalText: summaryText });
      return "";
    }

    if (!resp || !resp.content) {
      chatReply({ text: "Empty LLM response on turn " + (iter + 1), done: true, debugLink: dcDebugLink(iter + 1) });
      var emptyMsg = "FAILED at turn " + (iter + 1) + ": empty response";
      dcLogTurn({ n: iter + 1, resp: resp || {}, durationMs: _turnMs });
      dcFlush({ status: "empty_response", finalText: emptyMsg });
      return emptyMsg;
    }

    // Append the assistant message exactly as returned (Anthropic requires
    // signed content blocks to round-trip unchanged)
    messages.push({ role: "assistant", content: resp.content });

    // Surface the model's text-content thoughts (if any)
    var textParts = _findTextBlocks(resp.content);

    // Termination: stop_reason === "end_turn" means model is done.
    // Persist this invocation's turn to chat history before returning.
    if (resp.stop_reason === "end_turn") {
      var finalText = textParts.join("\n").trim();
      if (!finalText) finalText = "(model returned end_turn with no text)";
      // Surface any://spaceId/objectId links in the reply as structured
      // attachments. The wire shape is { id: { type, link } } — id is a
      // short opaque string (must match [A-Za-z0-9_-]+, ≤ 64 chars; we
      // use "a1", "a2", … to stay well under the cap) and `type` is
      // an open enum; here every match comes from a markdown link, so
      // they're all "link" — image extraction would happen elsewhere.
      var attachments = {};
      var attachCount = 0;
      var linkRe = /\(any:\/\/[^/]+\/([a-z2-7]{50,})\)/g;
      var linkMatch;
      var seenIds = {};
      while ((linkMatch = linkRe.exec(finalText)) !== null) {
        var objId = linkMatch[1];
        if (seenIds[objId]) continue;
        seenIds[objId] = true;
        attachCount++;
        attachments["a" + attachCount] = {
          type: "link",
          link: linkMatch[0].slice(1, -1), // strip the surrounding parens
        };
        if (attachCount >= 5) break;
      }
      if (attachCount > 0) {
        chatReply({ text: "✅ " + finalText, attachments: attachments, done: true, debugLink: dcDebugLink(iter + 1) });
      } else {
        chatReply({ text: "✅ " + finalText, done: true, debugLink: dcDebugLink(iter + 1) });
      }

      if (historyEnabled) {
        // replies = each intermediate chatReply (collected in turnThinkParts
        // as the loop iterated) followed by the final ✅ reply — mirrors the
        // chat surface, where each is a separate bubble. One append-only
        // record; compression (if due) runs inside persistTurn.
        var assistantEntries = [];
        for (var __ti = 0; __ti < turnThinkParts.length; __ti++) {
          var __t = turnThinkParts[__ti];
          if (__t) assistantEntries.push(__t);
        }
        if (finalText) assistantEntries.push(finalText);
        persistTurn(conv, currentChatId, buildTurnRec(assistantEntries, "end_turn"));
      }

      // Space-context split: only runs when the `_space_context` skill is
      // deployed (gate captured at bootstrap). All failures are swallowed —
      // the turn is already complete.
      if (spaceContextSkill) {
        try {
          maybeSplitSpaceContext(bootClient, console.log);
        } catch (e) {
          console.log("[space-context] split skipped: " + (e && e.message ? e.message : e));
        }
      }

      dcLogTurn({ n: iter + 1, resp: resp, durationMs: _turnMs, toolResults: [] });
      dcFlush({ status: "end_turn", finalText: finalText });
      return "";
    }

    // Surface non-terminal text blocks as chat replies — these are the
    // progress updates the model emits alongside tool_use ("let me check...",
    // "I see that..."). The final-turn text is handled above with the ✅
    // prefix, so this only fires on turns that still have tool_use to run.
    if (textParts.length > 0) {
      chatReply({ text: textParts.join("\n"), done: false, debugLink: dcDebugLink(iter + 1) });
      turnThinkParts.push(textParts.join("\n").trim());
    }

    // Otherwise: process tool_use blocks
    var toolBlocks = _findToolUseBlocks(resp.content);
    if (toolBlocks.length === 0) {
      // No text and no tool_use? Treat as termination with whatever we have.
      chatReply({ text: "(no tool_use and no text — terminating)", done: true, debugLink: dcDebugLink(iter + 1) });
      var fallbackText = textParts.join("\n") || "(no content)";
      dcLogTurn({ n: iter + 1, resp: resp, durationMs: _turnMs, toolResults: [] });
      dcFlush({ status: "no_content", finalText: fallbackText });
      return fallbackText;
    }

    var toolResults = [];
    for (var tbi = 0; tbi < toolBlocks.length; tbi++) {
      var execResult = executeToolUse(toolBlocks[tbi], args);
      toolResults.push(execResult.block);
      if (execResult.effects && execResult.effects.length > 0) {
        for (var efi = 0; efi < execResult.effects.length; efi++) {
          turnEffects.push(execResult.effects[efi]);
        }
      }
    }

    dcLogTurn({ n: iter + 1, resp: resp, durationMs: _turnMs, toolResults: toolResults });
    messages.push({ role: "user", content: toolResults });
  }

  })();  // end IIFE — _coreResult holds whatever the body returned

  if (__quiet) {
    var _cap = __captured.join("\n\n");
    return _cap || (typeof _coreResult === "string" ? _coreResult : "");
  }
  return _coreResult;
}
