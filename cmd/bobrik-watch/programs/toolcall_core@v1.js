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

import { createClient, getProp, extractMarkdownSection } from "anyHelper@v1";
import { createLLM } from "llm@v1";
import { displayValue, formatTraceOneLiner, summarizeTrace } from "utils@v1";
import { createAMemory } from "amemory@v2";
// No tracer import: Sonnet recovers from errors directly via is_error tool_result.
// The tracer added LLM calls for marginal benefit when the model is capable
// enough to fix its own broken cells in the next turn.

var llm = createLLM();

// ============================================================================
// CONSTANTS
// ============================================================================

// Per-tool_result char budget. When a cell's trace one-liner would push the
// returned tool_result content past this, we replace it with a counts+sample
// summary and stash the full one-liner array under the tool_use_id in the
// kernel-side `toolEffects` store. The agent fetches it next cell via
// toolEffects.get(toolUseId).
var MAX_TOOL_RESULT_CHARS = 16000;

// Rolling chat-history budget. Raw turns (user/think/assistant/effects
// markdown) accumulate in a single at_memory object. When the file crosses
// CHAT_MAX_CHARS, we Sonnet-compress the oldest ~CHAT_COMPRESS_CHARS into a
// chat_chunk memory and trim the file, so the live rolling view oscillates
// between CHAT_COMPRESS_CHARS and CHAT_MAX_CHARS.
var CHAT_MAX_CHARS = 20000;
var CHAT_COMPRESS_CHARS = 10000;

// How many most-recent compressed chunks to always inject at boot.
// Bumped up because the live rolling window only holds ~4-5 raw turns before
// compression kicks in; injecting more prior chunks restores long-range
// session continuity without reinflating the live window.
var CHAT_CHUNKS_TO_INJECT = 8;

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

function _stripMethodNameSuffix(name) {
  var parenIdx = name.indexOf("(");
  if (parenIdx > 0) return name.substring(0, parenIdx).trim();
  return name.trim();
}

// Walk a tool's full markdown and return { description, methods }, where
// methods = [{ name, bareName, content, kind }] sliced from "## Tool Schema"
// (or "# Tools") subsections.
//
// Recognized kind tags: getter, mutator, setup, program. Methods tagged
// `program` are included here; the boot path filters them out for anyHelper
// so they vanish from the discovery surface.
function _parseToolMarkdown(md) {
  if (!md) return { description: "", methods: [] };
  var description = extractMarkdownSection(md, "Tool Description") || "";
  var toolsStart = -1;
  var candidates = ["\n# Tools\n", "# Tools\n", "\n## Tools\n", "## Tools\n", "\n## Tool Schema\n", "## Tool Schema\n"];
  for (var ci = 0; ci < candidates.length; ci++) {
    toolsStart = md.indexOf(candidates[ci]);
    if (toolsStart !== -1) break;
  }
  if (toolsStart === -1) return { description: description, methods: [] };
  if (md.charAt(toolsStart) === "\n") toolsStart++;

  var lines = md.substring(toolsStart).split("\n");
  var sectionLevel = 1;
  var headM = lines[0] && lines[0].match(/^(#{1,6})\s/);
  if (headM) sectionLevel = headM[1].length;
  var methodPrefix = "";
  for (var h = 0; h <= sectionLevel; h++) methodPrefix += "#";
  methodPrefix += " ";

  var methods = [];
  var currentName = null;
  var currentKind = "getter";
  var currentLines = [];

  function flush() {
    if (currentName === null) return;
    methods.push({
      name: currentName,
      bareName: _stripMethodNameSuffix(currentName),
      content: currentLines.join("\n"),
      kind: currentKind
    });
  }

  for (var i = 1; i < lines.length; i++) {
    var hm = lines[i].match(/^(#{1,6})\s/);
    if (hm && hm[1].length <= sectionLevel) break;
    if (lines[i].indexOf(methodPrefix) === 0) {
      flush();
      var headingText = lines[i].substring(methodPrefix.length).trim();
      var kindMatch = headingText.match(/\s*\[(getter|mutator|setup|program)\]\s*$/);
      currentKind = "getter";
      if (kindMatch) {
        currentKind = kindMatch[1];
        headingText = headingText.substring(0, kindMatch.index).trim();
      }
      currentName = headingText;
      currentLines = [lines[i]];
    } else if (currentName !== null) {
      currentLines.push(lines[i]);
    }
  }
  flush();
  return { description: description, methods: methods };
}

// Build the per-session toolDocs table from getTools() results. Hard-errors
// if the anyHelper tool is not discoverable in the space — every kernel
// must have it as the `anyHelper` global.
function _buildToolDocs(bootClient, tools) {
  var toolDocs = {};
  for (var ti = 0; ti < tools.length; ti++) {
    var t = tools[ti];
    // Route the full-object fetch to the program's source space. Without this,
    // a system-space tool's id won't resolve from the user-space client.
    var fullObj = bootClient.getObject(t.id, t.space ? { space: t.space } : undefined);
    var md = fullObj && fullObj.markdown ? fullObj.markdown : "";
    var parsed = _parseToolMarkdown(md);
    var methods = parsed.methods;

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
      description: parsed.description,
      createdDate: (fullObj && fullObj.created_date) || "",
      methods: methods.map(function(m) { return { bareName: m.bareName, content: m.content }; })
    };
  }
  if (!toolDocs["anyHelper"]) {
    throw new Error("anyHelper not found in getTools() — every kernel boot requires it. Ensure the anyHelper tool is saved to the space and tagged any_tool.");
  }
  return toolDocs;
}

// For anyHelper specifically, additional method names that exist on the
// instance but should be hidden from the discovery surface. The .md may not
// document them at all (e.g. saveTool); we still want them out of listMethods.
var ANYHELPER_HIDDEN_METHODS = [
  "saveTool",
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

  // Per-toolUseId effect store. The host stashes a cell's full trace one-liner
  // array here when its tool_result would exceed MAX_TOOL_RESULT_CHARS; cells
  // fetch it back via toolEffects.get(id) (returns array of one-liner strings)
  // or list all stashed ids via toolEffects.list(). Cleared by js.reset() at
  // the start of each runToolcaller invocation.
  lines.push('globalThis._toolEffectsStore = {};');
  lines.push(
    'globalThis.toolEffects = {\n' +
    '  get: function(id) { return globalThis._toolEffectsStore[id] || null; },\n' +
    '  list: function() { return Object.keys(globalThis._toolEffectsStore); }\n' +
    '};'
  );

  return { code: lines.join("\n"), importFailures: failures };
}

// Build the dynamic "Tools available in this kernel" section of the system
// prompt. Per tool: ## heading + verbatim Tool Description + bare method names.
function _buildToolsPromptSection(toolDocs) {
  // Ordering discipline for the injected tool list:
  //   1. `anyHelper` pinned first.
  //   2. `amemory` pinned second.
  //   3. Everything else by Anytype `created_date` ascending (oldest first),
  //      so stable tools bubble up above newer, churning ones. Undated tools
  //      sort last. Alphabetical as final tiebreak.
  // ISO 8601 dates with `Z` suffix sort correctly as strings — the format
  // is fixed-width left-to-right most-significant, no parsing needed.
  var PIN_ORDER = { "anyHelper": 0, "amemory": 1 };
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
    var methodNames = t.methods.map(function(m) { return m.bareName; });
    if (methodNames.length > 0) {
      out += "Methods: " + methodNames.join(", ") + "\n\n";
    } else {
      out += "Methods: (none documented — call " + n + ".listMethods() at runtime)\n\n";
    }
  }
  out +=
    "## Discovering method signatures — required before each call\n\n" +
    "These modules are pre-bound as globals; do NOT import them and do NOT call them as Anthropic tools. The method NAMES for every module are already listed above — you do not need to call listMethods.\n\n" +
    "Before calling a method you have not used yet in this session, fetch its signature, inputs, outputs, and example with describeMethod (always inside a run_cell call):\n\n" +
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

// Fetch amemory's current categories at boot and render them into a short
// system-prompt section. Gives the agent a live inventory (builtins + any
// invented categories already in the space) so it can pick category filters
// without running listCategories() as its first run_cell. Counts omitted —
// they'd invalidate prompt cache on every memory write without helping the
// agent pick filters. Uses js.eval against the already-booted kernel where
// `amemory` is bound as a global. Fails silent (empty string) if amemory
// isn't available — e.g. not deployed yet, or an import error during boot.
function _fetchCategoriesSection() {
  try {
    var r = js.eval(
      "(function(){ try { if (typeof amemory !== 'object' || !amemory || typeof amemory.listCategories !== 'function') return null; return JSON.stringify(amemory.listCategories()); } catch(e) { return null; } })()",
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
    lines.push("\nUse `{categories: [...]}` on `amemory.search` to narrow retrieval. A category-filtered query surfaces items whose content wouldn't score close to the topic query in the shared embedding space (e.g. the user's general 'edit before create' preference won't match 'book reading notes', but `{categories: ['preference']}` will surface it).\n\n");
    return lines.join("\n");
  } catch (e) {
    return "";
  }
}


// ============================================================================
// Chat history — rolling markdown store + chunk injection
// ============================================================================

// Find or create the "_main" memory anchor. This is a single at_memory object
// that carries two non-amemory properties linking to (a) the rolling chat
// history object. We reuse v4's scheme so any existing anchor on the space
// keeps working. Returns { id, fullObj } or null.
function loadOrCreateMemoryAnchor(client) {
  var rawOpts = { resolveRefs: false };
  var objects = client.getObjects("agent_memory", rawOpts);
  for (var i = 0; i < objects.length; i++) {
    var obj = objects[i];
    if (getProp(obj, "agent_memory.agent_memory") === "_main") {
      return { id: obj.id, fullObj: client.getObject(obj.id, rawOpts) };
    }
  }

  var result = client.createObject("agent_memory", {
    name: "Agent Memory",
    body: "",
    properties: [{ key: "agent_memory", text: "_main" }]
  });
  if (!result || !result.ok) return null;
  return { id: result.object.id, fullObj: client.getObject(result.object.id, rawOpts) };
}

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

// Find or create the rolling chat-history object for a given chatId, linked
// from the memory anchor's `Agent Memory.chat_history` property (a multi-value
// `objects` field). Returns { id, markdown } or null on hard failure.
//
//   chatId    — the Anytype chat object id; null for legacy/unscoped mode
//   chatName  — optional, used only to name a newly-created history object
//   spaceType — used to enable one-time 1-1 adoption (see below)
//
// Lookup walks every linked history and matches by the
// `Agent Memory.chat_id` property. If none match and:
//   (a) chatId is set AND
//   (b) there's exactly one linked history with NO chat_id AND
//   (c) spaceType === 4 (OneToOne)
// we *adopt* that legacy object in place by patching its chat_id.
// This preserves 1-1 history continuity for users who had the agent deployed
// before chat scoping existed.
//
// chatId == null falls through to the legacy "first unkeyed" or "create new
// unkeyed" path — keeps old non-middleware callers working unchanged.
function loadOrCreateChatHistory(client, anchor, chatId, chatName, spaceType) {
  if (!anchor || !anchor.fullObj) return null;

  var val = getProp(anchor.fullObj, "agent_memory.chat_history");
  var ids = [];
  if (Array.isArray(val)) {
    for (var vi = 0; vi < val.length; vi++) {
      if (typeof val[vi] === "string" && val[vi].length > 10) ids.push(val[vi]);
    }
  } else if (typeof val === "string" && val.length > 10) {
    ids.push(val);
  }

  // Hydrate each linked history so we can match by chat_id.
  var hydrated = [];
  for (var i = 0; i < ids.length; i++) {
    var obj;
    try { obj = client.getObject(ids[i], { resolveRefs: false }); } catch (e) { obj = null; }
    if (obj) hydrated.push({ id: ids[i], obj: obj, chatId: getProp(obj, "agent_memory.chat_id") || null });
  }

  // Match by chatId (when set).
  if (chatId) {
    for (var mi = 0; mi < hydrated.length; mi++) {
      if (hydrated[mi].chatId === chatId) {
        return { id: hydrated[mi].id, markdown: hydrated[mi].obj.markdown || "" };
      }
    }
    // Legacy adoption for OneToOne: if exactly one unkeyed history exists,
    // stamp it with the current chatId and use it. Preserves 1-1 continuity.
    if (spaceType === 4) {
      var unkeyed = [];
      for (var ui = 0; ui < hydrated.length; ui++) {
        if (!hydrated[ui].chatId) unkeyed.push(hydrated[ui]);
      }
      if (unkeyed.length === 1) {
        try {
          client.updateObject(unkeyed[0].id, {
            properties: [{ key: "agent_memory.chat_id", text: chatId }]
          });
        } catch (e) {}
        return { id: unkeyed[0].id, markdown: unkeyed[0].obj.markdown || "" };
      }
    }
  } else if (hydrated.length > 0) {
    // Legacy path (no chatId supplied): reuse the first linked history
    // regardless of scoping, matching pre-scoping behaviour.
    return { id: hydrated[0].id, markdown: hydrated[0].obj.markdown || "" };
  }

  // No match — create a new history object for this chat.
  var historyName = "Agent Chat History";
  if (chatName) historyName += " — " + chatName;
  else if (chatId) historyName += " — " + chatId;

  var createProps = [];
  if (chatId) createProps.push({ key: "chat_id", text: chatId });

  var result = client.createObject("agent_memory", {
    name: historyName,
    body: "",
    properties: createProps
  });
  if (!result || !result.ok) return null;
  var newId = result.object.id;

  // Append (don't overwrite) the anchor's chat_history list so other chats'
  // histories stay linked.
  var existing = ids.slice();
  existing.push(newId);
  try {
    client.updateObject(anchor.id, {
      properties: [{ key: "agent_memory.chat_history", objects: existing }]
    });
  } catch (e) {}
  return { id: newId, markdown: "" };
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

// Turn-record format:
//   \n\n### <ISO-ts>\nuser> <text>\n[think> <text>\n][assistant> <text>\n][effects>\n<lines>\n]
// Each optional section is omitted if empty. We use a `### <ts>` markdown
// heading as the turn separator rather than `---` because Anytype's markdown
// layer rewrites horizontal rules (`---`) with a leading space on round-trip,
// which breaks literal splitting. Headings round-trip cleanly.
var TURN_DELIM_WRITE = "\n\n### ";     // literal used when formatting a new turn
var TURN_DELIM_READ = /\n+### /;       // permissive regex used when splitting a stored blob

function formatTurnRecord(record) {
  var lines = [];
  lines.push(TURN_DELIM_WRITE + (record.ts || new Date().toISOString()));
  if (record.user) {
    if (record.user_name) {
      lines.push("user (@" + record.user_name + ")> " + record.user);
    } else {
      lines.push("user> " + record.user);
    }
  }
  if (record.think) lines.push("think> " + record.think);
  // Assistant: array of discrete chat replies (intermediates + final), or a
  // single string for legacy turns. Each entry gets its own `assistant>` line
  // so chat history mirrors what the user saw — discrete bubbles in order.
  if (record.assistant) {
    var entries = Array.isArray(record.assistant) ? record.assistant : [record.assistant];
    for (var ai = 0; ai < entries.length; ai++) {
      var entry = entries[ai];
      if (!entry) continue;
      if (record.assistant_name) {
        lines.push("assistant (@" + record.assistant_name + ")> " + entry);
      } else {
        lines.push("assistant> " + entry);
      }
    }
  }
  if (record.effects && record.effects.length > 0) {
    lines.push("effects>");
    for (var i = 0; i < record.effects.length; i++) {
      lines.push(record.effects[i]);
    }
  }
  return lines.join("\n") + "\n";
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
    else if (bare === "saveProgram") op = "program-saved";
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

// Parse the rolling markdown into an ordered list of turn records. Each record:
//   { ts, user, think, assistant, effects[] }
// Unknown / legacy lines are ignored. Used both for injecting prior turns as
// alternating user/assistant messages AND for picking a split point during
// compression (we only split on `### <ts>` boundaries, never mid-turn).
function parseTurnsFromMarkdown(md) {
  if (!md) return [];
  var parts = md.split(TURN_DELIM_READ);
  var turns = [];
  for (var pi = 0; pi < parts.length; pi++) {
    var chunk = parts[pi];
    if (!chunk || chunk.trim() === "") continue;
    var lines = chunk.split("\n");
    // assistant is an array of discrete entries (intermediates + final); each
    // `assistant>` line opens a new entry, continuation lines append to it.
    var rec = { ts: "", user: "", user_name: "", think: "", assistant: [], assistant_name: "", effects: [] };
    var section = null;
    var bufs = { user: [], think: [], effects: [] };
    var asstCurrent = null;
    for (var li = 0; li < lines.length; li++) {
      var line = lines[li];
      if (li === 0 && /^\d{4}-\d{2}-\d{2}T/.test(line)) { rec.ts = line.trim(); continue; }
      var userMatch = /^user(?: \(@([^)]+)\))?> /.exec(line);
      if (userMatch) {
        if (asstCurrent !== null) { rec.assistant.push(asstCurrent.join("\n").trim()); asstCurrent = null; }
        section = "user";
        if (userMatch[1] && !rec.user_name) rec.user_name = userMatch[1];
        bufs.user.push(line.substring(userMatch[0].length));
        continue;
      }
      if (line.indexOf("think> ") === 0) {
        if (asstCurrent !== null) { rec.assistant.push(asstCurrent.join("\n").trim()); asstCurrent = null; }
        section = "think";
        bufs.think.push(line.substring(7));
        continue;
      }
      var asstMatch = /^assistant(?: \(@([^)]+)\))?> /.exec(line);
      if (asstMatch) {
        if (asstCurrent !== null) { rec.assistant.push(asstCurrent.join("\n").trim()); asstCurrent = null; }
        section = "assistant";
        if (asstMatch[1] && !rec.assistant_name) rec.assistant_name = asstMatch[1];
        asstCurrent = [line.substring(asstMatch[0].length)];
        continue;
      }
      if (line.indexOf("effects>") === 0) {
        if (asstCurrent !== null) { rec.assistant.push(asstCurrent.join("\n").trim()); asstCurrent = null; }
        section = "effects";
        continue;
      }
      if (section === "effects") {
        if (line.trim() !== "") bufs.effects.push(line);
        continue;
      }
      if (section === "assistant") {
        if (line !== "" && asstCurrent !== null) asstCurrent.push(line);
        continue;
      }
      if (section && line !== "" && bufs[section]) bufs[section].push(line);
    }
    if (asstCurrent !== null) rec.assistant.push(asstCurrent.join("\n").trim());
    rec.user = bufs.user.join("\n").trim();
    rec.think = bufs.think.join("\n").trim();
    rec.effects = bufs.effects;
    if (rec.user || rec.assistant.length > 0 || rec.effects.length > 0) turns.push(rec);
  }
  return turns;
}

// Compression: when markdown > CHAT_MAX_CHARS, split off the oldest
// ~CHAT_COMPRESS_CHARS at a turn boundary, Sonnet-summarize, and persist a
// chat_chunk memory with the span.
//
// Returns { compressed_turn_count, keptMarkdown } — caller is responsible
// for writing keptMarkdown back to the history object.
function compressOldestTurns(amem, markdown, chatId) {
  // Ensure a leading newline so the very first `### ts` matches the split regex.
  var normMd = markdown.charAt(0) === "\n" ? markdown : "\n" + markdown;
  var parts = normMd.split(TURN_DELIM_READ);
  // parts[0] is the leading prefix before the first \n---\n (normally "").
  // parts[1..] are the turn bodies. Walk from parts[1] forward, accumulating
  // until we'd exceed CHAT_COMPRESS_CHARS — THAT part (and everything after)
  // stays in keptParts. This guarantees the current (newest) turn is always
  // retained even when the rest overflows.
  var toCompressParts = [];
  var toKeepParts = [];
  var compressedChars = 0;
  var crossedThreshold = false;

  if (parts.length <= 1) {
    return { compressed_turn_count: 0, keptMarkdown: markdown, skipped: true };
  }

  for (var i = 1; i < parts.length; i++) {
    var p = parts[i];
    // Always keep the LAST part (current turn). Never compress it — even if
    // it alone exceeds the threshold, the goal of compression is to bound the
    // history, not to discard the latest state.
    var isLast = (i === parts.length - 1);
    if (isLast) {
      crossedThreshold = true;
      toKeepParts.push(p);
      continue;
    }
    if (!crossedThreshold && (compressedChars + p.length) <= CHAT_COMPRESS_CHARS) {
      toCompressParts.push(p);
      compressedChars += p.length + 5; // "\n---\n"
    } else {
      crossedThreshold = true;
      toKeepParts.push(p);
    }
  }

  if (toCompressParts.length === 0) {
    return { compressed_turn_count: 0, keptMarkdown: markdown, skipped: true };
  }

  var toCompressMd = TURN_DELIM_WRITE + toCompressParts.join(TURN_DELIM_WRITE);
  var turns = parseTurnsFromMarkdown(toCompressMd);
  if (turns.length === 0) {
    return { compressed_turn_count: 0, keptMarkdown: markdown, skipped: true, reason: "parseTurnsFromMarkdown returned 0 turns" };
  }

  var periodStart = turns[0].ts || new Date().toISOString();
  var periodEnd = turns[turns.length - 1].ts || periodStart;

  var prompt = "Summarize the following chat history slice into 5-8 sentences. " +
    "Preserve: (a) what the user asked for, (b) what objects were created/updated/deleted with their TYPES and NAMES and IDs when available, " +
    "(c) any unresolved threads or pending follow-ups, (d) any long-form artifact the assistant stashed in an Anytype object (include its name/type/id so a future turn can locate it). " +
    "Be terse but dense — this is a memory aid for a future assistant turn, not a narrative, and several of these summaries will be concatenated so each one must stand on its own. " +
    "Output plain text only, no headers, no markdown.\n\n" +
    "CHAT HISTORY SLICE:\n" + toCompressMd;

  var summary = null;
  try {
    // Compression uses the resolved tier model (no override). The prompt asks
    // for 2-4 sentences so the model naturally returns ~200 tokens; the
    // chat-call default max_tokens is a safe ceiling against silent truncation.
    var resp = llm.chat([{ role: "user", content: prompt }], {});
    if (resp && resp.content) {
      var parts2 = [];
      for (var ci = 0; ci < resp.content.length; ci++) {
        if (resp.content[ci].type === "text") parts2.push(resp.content[ci].text);
      }
      summary = parts2.join("\n").trim();
    }
  } catch (e) {
    return { compressed_turn_count: 0, keptMarkdown: markdown, error: "compression LLM failed: " + (e && e.message ? e.message : String(e)) };
  }

  if (!summary) {
    return { compressed_turn_count: 0, keptMarkdown: markdown, error: "compression produced empty summary" };
  }

  var chunkResult = amem.createChatChunk({
    period_start: periodStart,
    period_end: periodEnd,
    summary: summary,
    turns_covered: turns.length,
    chatId: chatId || ""
  });
  if (!chunkResult || !chunkResult.ok) {
    return { compressed_turn_count: 0, keptMarkdown: markdown, error: "createChatChunk failed: " + (chunkResult && chunkResult.error) };
  }

  // Rejoin kept parts. Prefix with the write delimiter so each kept turn
  // starts with `### <ts>` (matching the original format) and future parses
  // round-trip.
  var kept = toKeepParts.length > 0
    ? TURN_DELIM_WRITE + toKeepParts.join(TURN_DELIM_WRITE)
    : "";
  return {
    compressed_turn_count: turns.length,
    keptMarkdown: kept,
    chunkId: chunkResult.id,
    periodStart: periodStart,
    periodEnd: periodEnd
  };
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
function _formatHistoryTs(isoTs) {
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

// Render prior turns from the live markdown window into an ordered list of
// alternating {role, content} messages for the conversation. Each turn
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
function renderWindowMessages(turns) {
  var msgs = [];
  for (var i = 0; i < turns.length; i++) {
    var t = turns[i];
    var ts = _formatHistoryTs(t.ts);
    var tsPrefix = ts ? "[" + ts + "]\n" : "";

    var userBlocks = [];
    if (t.user) {
      var userLine = t.user_name ? "(@" + t.user_name + ") " + t.user : t.user;
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
    // assistant is an array of discrete chat replies; join them into a single
    // assistant message to preserve Anthropic's user/assistant alternation.
    // Legacy turns may still arrive as a string — handle both shapes.
    // Strip any leading `[Wkd YYYY-MM-DD HH:MM UTC]\n` from each stored entry —
    // older turns persisted before the render-side fix may carry that prefix
    // baked into the text the model wrote.
    var asstEntries = Array.isArray(t.assistant) ? t.assistant : (t.assistant ? [t.assistant] : []);
    var cleanedAsst = [];
    for (var ai = 0; ai < asstEntries.length; ai++) {
      var cleaned = _stripTsPrefix(asstEntries[ai]);
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
// (private) space. Lets users override a system skill (e.g. _anytype) by
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
function _loadAnytypeSkill(client) { return _loadSkillMarkdown(client, "_anytype"); }
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

// Build the turn-0 "earlier context" user message from compressed chunks.
// Rendered oldest-first (chronological). Returns null if no chunks.
function renderChunksMessage(chunks) {
  if (!chunks || chunks.length === 0) return null;
  var lines = ["[Earlier context, compressed]"];
  for (var i = 0; i < chunks.length; i++) {
    var c = chunks[i];
    var ps = (c.period_start || "").substring(0, 16);
    var pe = (c.period_end || "").substring(0, 16);
    var body = c.body || c.context || "";
    body = body.replace(/\n+/g, " ").trim();
    lines.push("— " + ps + ".." + pe + " — " + body);
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
// values, so nested arrays/objects (cells, messages, response) round-trip
// as-is. All writes are best-effort: if the agent space lacks the type or a
// write fails, the agent run continues unaffected.

var DC_DATASET = "agent_debug_log";

var _dc = {
  client: null,
  pageId: null,
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
  _dc.client = null; _dc.pageId = null; _dc.userText = "";
  _dc.startMs = 0; _dc.model = ""; _dc.turnCount = 0; _dc.seq = 0;
  _dc.totalIn = 0; _dc.totalOut = 0; _dc.totalCost = 0;
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

function dcInit(client, _spaceId, userText, bootMeta) {
  _dcReset();
  _dc.client = client;
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

// Record the full system prompt the LLM sees on every turn. Per-turn
// `messages` arrays are captured in dcLogTurn; this fills the only otherwise-
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

  // Turn record holds the extracted scalars + the per-cell code/result.
  // The full `messages[]` and raw `response{}` are intentionally NOT stored —
  // they're redundant with `cells[]` (and the prior turns' records) and balloon
  // the dataset. stopReason / tokens / model carry the useful response bits.
  _dcWriteEntry("turn", {
    n: n,
    stopReason: resp.stop_reason || "",
    durationMs: durationMs,
    inTokens: inT,
    outTokens: outT,
    cost: cost,
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
// tool_result block. Two sections:
//   - Last value: displayValue of the cell's final expression (strings in full;
//                 objects via inferSchema — see utils@v1)
//   - Effects: trace one-liner of API calls / wrapped helpers

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

// Render the summary form of an effects block — counts grouped by signature,
// first/last sample, plus the toolEffects.get hint. isError prefixes the
// header with "before the error" so the cell-failed path stays distinguishable.
function _formatEffectsSummary(summary, toolUseId, isError) {
  var header = (isError ? "Tool Effects that ran before the error" : "Tool Effects") +
    ": " + summary.totalCalls + " calls in this cell (" + toolUseId +
    ", inline trace summarized — full trace stashed)";
  var lines = [header];
  for (var i = 0; i < summary.groupLines.length; i++) {
    lines.push(summary.groupLines[i]);
  }
  lines.push("");
  if (summary.firstLine) lines.push("First: " + summary.firstLine);
  if (summary.lastLine && summary.lastLine !== summary.firstLine) {
    lines.push("Last:  " + summary.lastLine);
  }
  lines.push("");
  lines.push("Full trace stashed. Query in next cell:");
  lines.push("  toolEffects.get(\"" + toolUseId + "\")  // array of one-liner strings, in call order");
  lines.push("  toolEffects.list()                       // all stashed tool_use_ids in this session");
  lines.push("Treat the array as plain JS data — filter / inspect with normal array methods.");
  return lines.join("\n");
}

// Build the Effects content for a tool_result. Returns the full one-liner
// inline when it fits MAX_TOOL_RESULT_CHARS, or a counts+sample summary when
// it doesn't (in which case the full one-liner array is stashed under the
// tool_use_id in the kernel store).
function _buildEffectsContent(traces, toolUseId, isError) {
  var oneLiner = formatTraceOneLiner(traces);
  if (!oneLiner) return "";
  var header = isError ? "Tool Effects that ran before the error" : "Tool Effects";
  if (oneLiner.length > MAX_TOOL_RESULT_CHARS && toolUseId) {
    var summary = summarizeTrace(traces);
    if (summary && summary.totalCalls > 0) {
      _stashEffectsToKernel(toolUseId, oneLiner.split("\n"));
      return _formatEffectsSummary(summary, toolUseId, isError);
    }
  }
  return header + ":\n" + oneLiner;
}

function formatToolResult(result, toolUseId) {
  if (!result) return "(no result)";
  var parts = [];

  if (result.lastValue !== undefined && result.lastValue !== null) {
    parts.push("Last value: " + displayValue(result.lastValue));
  }

  // result.callTrace — per-call trace scoped to THIS eval invocation
  // (includes mock-hit calls, unlike traceDiff). result.traces is the
  // cumulative session log and would leak prior cells' effects into
  // this cell's Effects block. See docs/runtime-task-per-call-traces.md.
  var traces = _prepareTracesForLLM(result.callTrace || {});
  if (Object.keys(traces).length > 0) {
    var effectsContent = _buildEffectsContent(traces, toolUseId, false);
    if (effectsContent) parts.push(effectsContent);
  }

  if (parts.length === 0) parts.push("(cell completed, no return value, no effects)");
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

  // On any error, return is_error: true with the error message + any partial
  // trace from before the error fired.
  if (result && result.error) {
    var errContent = "Error: " + result.error;
    if (result.callTrace && Object.keys(result.callTrace).length > 0) {
      var partialFiltered = _prepareTracesForLLM(result.callTrace);
      var partialContent = _buildEffectsContent(partialFiltered, block.id, true);
      if (partialContent) errContent += "\n\n" + partialContent;
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
    systemSpaceId: args.systemSpaceId,
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
  var currentChatName = gate.chatName || null;

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
    dcFlush({ status: "bootstrap_failed", finalText: bootMsg });
    return bootMsg;
  }

  // Build the system prompt from required agent skills deployed in the space.
  // Both `_anytype` (identity + Anytype mechanics) and `_toolcaller` (run_cell
  // semantics + cell patterns) are load-bearing — fail hard with a clear
  // user-visible message if either is missing. No stale in-code fallback:
  // the skills in assistant-skills/*.md are the single source of truth.
  var anytypeSkill = _loadAnytypeSkill(bootClient);
  if (!anytypeSkill) {
    var missA = "System skill `_anytype` is missing from this space. Re-run the bobrik-watch bootstrap (`bobrik-watch --bootstrap`, or `kill -HUP $(cat .bobrik-pid)`) to deploy agent skills.";
    chatReply(missA);
    dcFlush({ status: "skill_missing", finalText: missA });
    return "";
  }
  var toolcallerSkill = _loadToolcallerSkill(bootClient);
  if (!toolcallerSkill) {
    var missT = "System skill `_toolcaller` is missing from this space. Re-run the bobrik-watch bootstrap (`bobrik-watch --bootstrap`, or `kill -HUP $(cat .bobrik-pid)`) to deploy agent skills.";
    chatReply(missT);
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
    anytypeSkill + "\n\n---\n\n" +
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
  // Load the rolling chat-history markdown + the most recent compressed chunks.
  // Both are rendered as prior conversation messages so the model sees its own
  // past work when answering follow-ups.
  //
  // In __quiet (sub-agent) mode we deliberately skip the load — the sub-agent
  // task is isolated, must not see chat history, and must not persist anything
  // to it. historyId stays null so the persist block below also no-ops.
  var amem = createAMemory(bootClient, { enableLinks: false });
  var anchor = loadOrCreateMemoryAnchor(bootClient);
  var history = __quiet
    ? null
    : loadOrCreateChatHistory(bootClient, anchor, currentChatId, currentChatName, spaceType);
  var historyId = history ? history.id : null;
  var historyMarkdown = history ? history.markdown : "";

  // Scope the injected chunk window to the current chat when we know it.
  // Cross-chat chunks are still reachable via amemory.search from cells.
  // Sub-agent runs (__quiet) skip the chunk window for isolation.
  var priorChunks = [];
  if (!__quiet) {
    try {
      var chunkOpts = { n: CHAT_CHUNKS_TO_INJECT };
      if (currentChatId) {
        chunkOpts.chatId = currentChatId;
        chunkOpts.chatIdFilter = "only";
      }
      priorChunks = amem.getRecentChatChunks(chunkOpts);
    } catch (e) {}
  }

  var messages = [];
  var chunkMsg = renderChunksMessage(priorChunks);
  if (chunkMsg) messages.push(chunkMsg);

  var priorTurns = parseTurnsFromMarkdown(historyMarkdown);
  var windowMsgs = renderWindowMessages(priorTurns);
  for (var wmi = 0; wmi < windowMsgs.length; wmi++) messages.push(windowMsgs[wmi]);

  // Current user ask — prefix with the same `[Wkd YYYY-MM-DD HH:MM UTC]`
  // header used for prior turns, so every message the model sees carries
  // its own timestamp.
  var nowTs = _formatHistoryTs(invocationStart);
  var nowPrefix = nowTs ? "[" + nowTs + "]\n" : "";
  var userLineNow = senderName ? "(@" + senderName + ") " + args.text : args.text;
  messages.push({ role: "user", content: nowPrefix + userLineNow });

  // Per-invocation accumulators (written to history on end_turn).
  var turnThinkParts = [];
  var turnEffects = [];

  for (var iter = 0; ; iter++) {
    var resp;
    var _t = Date.now();
    var _messagesSnapshot = messages.slice();
    try {
      resp = llm.chat(messages, {
        system: systemBlocks,
        tools: [RUN_CELL_TOOL]
      });
    } catch (e) {
      chatReply("LLM error on turn " + (iter + 1) + ": " + (e.message || e));
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
        chatReply("⚠ max_tokens — recovery summary failed: " + (e.message || e));
        dcLogTurn({ n: iter + 1, messages: _messagesSnapshot, resp: resp, durationMs: _turnMs, toolResults: maxTokenResults });
        dcFlush({ status: "max_tokens_summary_failed", finalText: sumErr });
        return sumErr;
      }

      var summaryText = "";
      if (summaryResp && summaryResp.content) {
        summaryText = _findTextBlocks(summaryResp.content).join("\n").trim();
      }
      if (!summaryText) summaryText = "(turn cut off by max_tokens; no summary produced)";
      chatReply("⚠ max_tokens — turn auto-closed:\n\n" + summaryText);

      // Persist as a normal turn so chat history shows what happened.
      if (historyId) {
        var assistantEntries = [];
        for (var __ti = 0; __ti < turnThinkParts.length; __ti++) {
          if (turnThinkParts[__ti]) assistantEntries.push(turnThinkParts[__ti]);
        }
        assistantEntries.push("[max_tokens auto-summary] " + summaryText);
        var turnRec = {
          ts: invocationStart,
          user: args.text,
          user_name: senderName || "",
          assistant: assistantEntries,
          assistant_name: botName || "",
          effects: turnEffects
        };
        var entry = formatTurnRecord(turnRec);
        var newMarkdown = (historyMarkdown || "") + entry;
        if (newMarkdown.length > CHAT_MAX_CHARS) {
          var comp = compressOldestTurns(amem, newMarkdown, currentChatId);
          if (comp && comp.compressed_turn_count > 0) newMarkdown = comp.keptMarkdown;
        }
        try {
          bootClient.updateObject(historyId, { markdown: newMarkdown });
        } catch (e) {}
      }

      dcLogTurn({ n: iter + 1, messages: _messagesSnapshot, resp: resp, durationMs: _turnMs, toolResults: maxTokenResults });
      dcFlush({ status: "max_tokens_summary", finalText: summaryText });
      return "";
    }

    if (!resp || !resp.content) {
      chatReply("Empty LLM response on turn " + (iter + 1));
      var emptyMsg = "FAILED at turn " + (iter + 1) + ": empty response";
      dcLogTurn({ n: iter + 1, messages: _messagesSnapshot, resp: resp || {}, durationMs: _turnMs });
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
        chatReply({ text: "✅ " + finalText, attachments: attachments });
      } else {
        chatReply("✅ " + finalText);
      }

      if (historyId) {
        // assistant is now an array — each intermediate chatReply (collected
        // in turnThinkParts as the loop iterated) becomes its own discrete
        // `assistant>` line, followed by the final ✅ reply. Mirrors the
        // chat surface, where each is a separate bubble.
        var assistantEntries = [];
        for (var __ti = 0; __ti < turnThinkParts.length; __ti++) {
          var __t = turnThinkParts[__ti];
          if (__t) assistantEntries.push(__t);
        }
        if (finalText) assistantEntries.push(finalText);
        var turnRec = {
          ts: invocationStart,
          user: args.text,
          user_name: senderName || "",
          assistant: assistantEntries,
          assistant_name: botName || "",
          effects: turnEffects
        };
        var entry = formatTurnRecord(turnRec);
        var newMarkdown = (historyMarkdown || "") + entry;

        // Overflow → compress oldest turns. compressOldestTurns receives the
        // already-appended newMarkdown; its keptMarkdown contains the trailing
        // (uncompressed) turns, including the one we just appended.
        if (newMarkdown.length > CHAT_MAX_CHARS) {
          console.log("[mem] chat history " + newMarkdown.length + " chars, compressing oldest...");
          var comp = compressOldestTurns(amem, newMarkdown, currentChatId);
          if (comp && comp.compressed_turn_count > 0) {
            console.log("[mem] compressed " + comp.compressed_turn_count + " turns → " + comp.chunkId +
                        " (" + comp.periodStart.substring(0, 16) + ".." + comp.periodEnd.substring(0, 16) + ")");
            newMarkdown = comp.keptMarkdown;
          } else if (comp && comp.error) {
            console.log("[mem] compression skipped: " + comp.error);
          }
        }

        try {
          bootClient.updateObject(historyId, { markdown: newMarkdown });
        } catch (e) {
          console.log("[mem] failed to persist chat history: " + (e && e.message ? e.message : e));
        }
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

      dcLogTurn({ n: iter + 1, messages: _messagesSnapshot, resp: resp, durationMs: _turnMs, toolResults: [] });
      dcFlush({ status: "end_turn", finalText: finalText });
      return "";
    }

    // Surface non-terminal text blocks as chat replies — these are the
    // progress updates the model emits alongside tool_use ("let me check...",
    // "I see that..."). The final-turn text is handled above with the ✅
    // prefix, so this only fires on turns that still have tool_use to run.
    if (textParts.length > 0) {
      chatReply(textParts.join("\n"));
      turnThinkParts.push(textParts.join("\n").trim());
    }

    // Otherwise: process tool_use blocks
    var toolBlocks = _findToolUseBlocks(resp.content);
    if (toolBlocks.length === 0) {
      // No text and no tool_use? Treat as termination with whatever we have.
      chatReply("(no tool_use and no text — terminating)");
      var fallbackText = textParts.join("\n") || "(no content)";
      dcLogTurn({ n: iter + 1, messages: _messagesSnapshot, resp: resp, durationMs: _turnMs, toolResults: [] });
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

    dcLogTurn({ n: iter + 1, messages: _messagesSnapshot, resp: resp, durationMs: _turnMs, toolResults: toolResults });
    messages.push({ role: "user", content: toolResults });
  }

  })();  // end IIFE — _coreResult holds whatever the body returned

  if (__quiet) {
    var _cap = __captured.join("\n\n");
    return _cap || (typeof _coreResult === "string" ? _coreResult : "");
  }
  return _coreResult;
}
