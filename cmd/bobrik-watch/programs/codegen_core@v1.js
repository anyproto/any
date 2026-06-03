// __main_source
// codegen_core@v1 — minimal CodeAct agent that incrementally synthesizes a program
// from named, traced helper functions in a persistent JS kernel.
//
// Three advisory phases (model self-declares):
//   explore → wrap probes in named functions, learn data shapes
//   build   → define getter/transformer helpers, test inline (runtime auto-wraps)
//   apply   → call registered helpers from a final orchestration step
//
// Depends on runtime support for:
//   js.eval(code, args, { persistent: true, enableWrapMethods: true })
//   js.reset()
//   done(value), ask(question)  — exposed as effects on the persistent child
//
// Run: anytype-agent-runtime -m systemjs assistantjs/codegen_core@v1.js text="..."

import { createClient } from "anyHelper@v1";
import { createLLM } from "llm@v1";
import { trace as runTracer, detectError } from "tracer@v1";
import { extractToolMethods } from "assistant@v4";
import { inferSchema, formatTraceOneLiner } from "utils@v1";

var llm = createLLM();

// ============================================================================
// CONSTANTS
// ============================================================================
var MAX_ITERS = 30;

// Trace keys hidden from the codegen prompt at render time.
// Filter is one-way: hidden keys are still recorded in the trace, just not shown
// to the model. Tracer.js (when called for repair) sees the unfiltered trace.
//
// done/ask are hidden because they're control flow (read directly from
// result.action by the agent loop), not observations. Showing them as
// `done("call") → "answer"` in the step log would just be noise.
var HIDDEN_KEYS = ["fetch", "fetchBatch", "sleep", "console.log", "done", "ask"];
var HIDDEN_PREFIXES = ["anyHelper."];

// Method names from anyHelper.md that we deliberately drop from codegen_core's
// tool docs even though they're tagged [setup]. The "setup" method shows the
// v4-style `import { createClient } from ...; var client = createClient({...})`
// pattern as an example, which the model copies into its cells despite the
// KERNEL_API explicitly forbidding `import` and `function main()`. Since
// codegen_core pre-binds `client` at bootstrap, the setup example is actively
// misleading. Other [setup] methods (property_types, key_concepts) are kept —
// they're useful and don't show forbidden patterns.
var EXCLUDED_METHOD_NAMES = ["setup"];

// inferSchema moved to utils@v1 (imported above). The kernel pulls it via
// `__import("utils@v1").inferSchema` in the bootstrap below — no source-string
// duplication needed.

// ============================================================================
// extractDescriptionAbove — backward line scanner for // and /* */ comments
// ============================================================================
// Walks `source` backward from the line where `var <fnName> = function`,
// `function <fnName>(`, or `<fnName> = function` appears, collecting comment
// lines until a non-comment, non-blank line is hit.

export function extractDescriptionAbove(source, fnName) {
  if (!source || !fnName) return "";
  var lines = source.split("\n");

  // Match any of: var X = function/=> | function X( | X = function/=>
  var pattern = new RegExp(
    "(?:^|\\s)(?:var\\s+|let\\s+|const\\s+)?" + fnName + "\\s*=\\s*(?:function|\\()" +
    "|(?:^|\\s)function\\s+" + fnName + "\\s*\\("
  );

  var defLineIdx = -1;
  for (var i = 0; i < lines.length; i++) {
    if (pattern.test(lines[i])) { defLineIdx = i; break; }
  }
  if (defLineIdx === -1) return "";

  // Walk backward, collect comment text
  var collected = [];  // strings, will be joined in reverse
  var i2 = defLineIdx - 1;
  while (i2 >= 0) {
    var line = lines[i2];
    var trimmed = line.trim();

    if (trimmed === "") {
      // Blank line — keep walking, but if we already collected, stop
      if (collected.length > 0) break;
      i2--;
      continue;
    }

    // Single-line // comment
    if (trimmed.indexOf("//") === 0) {
      collected.push(trimmed.replace(/^\/+\s*/, ""));
      i2--;
      continue;
    }

    // Single-line /* ... */ comment
    if (trimmed.indexOf("/*") === 0 && trimmed.indexOf("*/") === trimmed.length - 2) {
      var inner = trimmed.substring(2, trimmed.length - 2).trim();
      // Strip leading * for JSDoc-style single-line
      inner = inner.replace(/^\*+\s*/, "");
      collected.push(inner);
      i2--;
      continue;
    }

    // Multi-line block comment closing line: contains "*/" but not "/*"
    if (trimmed.indexOf("*/") !== -1 && trimmed.indexOf("/*") === -1) {
      // Walk further back collecting until we hit the opening "/*"
      var blockLines = [];
      var lastSeg = trimmed.substring(0, trimmed.indexOf("*/")).trim();
      lastSeg = lastSeg.replace(/^\*+\s*/, "");
      if (lastSeg) blockLines.push(lastSeg);
      i2--;
      while (i2 >= 0) {
        var bl = lines[i2].trim();
        if (bl.indexOf("/*") !== -1) {
          // Opening line — capture content after /*
          var firstSeg = bl.substring(bl.indexOf("/*") + 2).trim();
          firstSeg = firstSeg.replace(/^\*+\s*/, "");
          if (firstSeg) blockLines.push(firstSeg);
          i2--;
          break;
        }
        blockLines.push(bl.replace(/^\*+\s*/, ""));
        i2--;
      }
      // Reverse the block (we collected bottom-up) and add as one string
      blockLines.reverse();
      collected.push(blockLines.join(" "));
      continue;
    }

    // Hit a non-comment, non-blank line — stop
    break;
  }

  // collected is in reverse order (bottom-up); reverse for natural reading
  collected.reverse();
  return collected.join(" ").trim();
}

// ============================================================================
// extractSignature — grab the parameter list of a function definition
// ============================================================================

export function extractSignature(source, fnName) {
  if (!source || !fnName) return "()";
  // Match the opening paren after the function name
  var patterns = [
    new RegExp("(?:var|let|const)\\s+" + fnName + "\\s*=\\s*function\\s*\\(([^)]*)\\)"),
    new RegExp("function\\s+" + fnName + "\\s*\\(([^)]*)\\)"),
    new RegExp("(?:var|let|const)\\s+" + fnName + "\\s*=\\s*\\(([^)]*)\\)\\s*=>"),
    new RegExp(fnName + "\\s*=\\s*function\\s*\\(([^)]*)\\)")
  ];
  for (var pi = 0; pi < patterns.length; pi++) {
    var m = source.match(patterns[pi]);
    if (m) return "(" + m[1].trim() + ")";
  }
  return "()";
}

// ============================================================================
// filterTraceForCodegen — drop hidden keys before showing trace to model
// ============================================================================

export function filterTraceForCodegen(traces) {
  if (!traces) return {};
  var visible = {};
  for (var k in traces) {
    if (HIDDEN_KEYS.indexOf(k) !== -1) continue;
    var hidden = false;
    for (var pi = 0; pi < HIDDEN_PREFIXES.length; pi++) {
      if (k.indexOf(HIDDEN_PREFIXES[pi]) === 0) { hidden = true; break; }
    }
    if (!hidden) visible[k] = traces[k];
  }
  return visible;
}

// formatTraceOneLiner moved to utils@v1 (imported above).

// ============================================================================
// mergeTraces — accumulate per-turn traces into the cumulative trace
// ============================================================================

export function mergeTraces(cumulative, turnTraces) {
  if (!turnTraces) return;
  for (var k in turnTraces) {
    if (!cumulative[k]) cumulative[k] = {};
    var rec = turnTraces[k];
    for (var input in rec) {
      if (!cumulative[k][input]) cumulative[k][input] = [];
      var outs = rec[input];
      for (var oi = 0; oi < outs.length; oi++) cumulative[k][input].push(outs[oi]);
    }
  }
}

// ============================================================================
// mostRecentTraceEntry — get last (input, output) pair for a function name
// ============================================================================

export function mostRecentTraceEntry(cumulativeTrace, fnName) {
  var rec = cumulativeTrace[fnName];
  if (!rec) return null;
  var keys = Object.keys(rec);
  if (keys.length === 0) return null;
  var lastKey = keys[keys.length - 1];
  var outputs = rec[lastKey];
  if (!outputs || outputs.length === 0) return null;
  return { input: lastKey, output: outputs[outputs.length - 1] };
}

// ============================================================================
// parseStructured — parse the LLM's structured output
// ============================================================================
// Tolerant: tries strict JSON first, then markdown-fence-wrapped JSON, then
// markdown code block extraction. The model is told to output JSON, but we
// fall back to markdown extraction so a slightly off response still works.
//
// Also strips Qwen-style <think>...</think> blocks before parsing — those
// are reasoning traces, not part of the structured output.

export function parseStructured(text) {
  if (!text) return { thought: "", code: "", phase: null };

  // Strip <think> blocks (Qwen-style reasoning)
  text = text.replace(/<think>[\s\S]*?<\/think>/g, "").trim();

  // Try strict JSON parse
  var stripped = text.replace(/^```(?:json)?\s*/m, "").replace(/\s*```\s*$/m, "").trim();
  try {
    var obj = JSON.parse(stripped);
    if (obj && typeof obj.code === "string") {
      return {
        thought: typeof obj.thought === "string" ? obj.thought : "",
        code: obj.code,
        phase: typeof obj.phase === "string" ? obj.phase : null
      };
    }
  } catch (e) { /* fall through */ }

  // Fallback: extract markdown code block
  var fenceStart = text.indexOf("```javascript");
  if (fenceStart === -1) fenceStart = text.indexOf("```js");
  if (fenceStart === -1) fenceStart = text.indexOf("```");
  if (fenceStart === -1) {
    return { thought: text, code: "", phase: null };
  }
  var codeStart = text.indexOf("\n", fenceStart);
  if (codeStart === -1) return { thought: text, code: "", phase: null };
  codeStart += 1;
  var codeEnd = text.indexOf("```", codeStart);
  var code = codeEnd === -1 ? text.substring(codeStart) : text.substring(codeStart, codeEnd);
  var thoughtText = text.substring(0, fenceStart).trim();

  // Look for phase declaration in thought text
  var phase = null;
  var phaseMatch = thoughtText.match(/\b(?:phase|PHASE)\s*[:=]\s*["']?(explore|build|apply)["']?/i);
  if (phaseMatch) phase = phaseMatch[1].toLowerCase();

  return { thought: thoughtText, code: code, phase: phase };
}

// ============================================================================
// snapshotGlobals — list current top-level globals on the persistent kernel
// ============================================================================
// Returns array of names. Used to detect new bindings after each turn.

function snapshotGlobals() {
  var probe = js.eval("Object.keys(globalThis)", {}, { persistent: true });
  if (probe && probe.lastValue && Array.isArray(probe.lastValue)) return probe.lastValue;
  // Fallback path if lastValue isn't populated
  if (probe && probe.result && Array.isArray(probe.result)) return probe.result;
  return [];
}

// ============================================================================
// probeVariableValues — single combined js.eval call for all registered vars
// ============================================================================
// Returns a map of {name: inferSchemaResult}. Re-probed at every prompt build
// so mutations are reflected. Variables that have been deleted report null.

function probeVariableValues(names) {
  if (!names || names.length === 0) return {};
  var pairs = [];
  for (var i = 0; i < names.length; i++) {
    var n = names[i];
    pairs.push(n + ": (typeof " + n + " !== 'undefined' ? inferSchema(" + n + ") : null)");
  }
  var expr = "({" + pairs.join(", ") + "})";
  var result = js.eval(expr, {}, { persistent: true });
  if (result && result.lastValue) return result.lastValue;
  if (result && result.result) return result.result;
  return {};
}

// ============================================================================
// Tool docs rendering — filtered by phase
// ============================================================================

function _stripMethodNameSuffix(name) {
  // "setup" → "setup"; "createObject(typeKey, data?)" → "createObject"
  var parenIdx = name.indexOf("(");
  if (parenIdx > 0) return name.substring(0, parenIdx).trim();
  return name.trim();
}

function renderToolDocs(allMethods, allowedKinds) {
  var sections = [];
  for (var toolId in allMethods) {
    var tool = allMethods[toolId];
    var filtered = [];
    for (var mi = 0; mi < tool.methods.length; mi++) {
      var m = tool.methods[mi];
      if (allowedKinds.indexOf(m.kind) === -1) continue;
      // Drop methods on the codegen_core exclusion list (e.g. "setup" — its
      // example shows the v4 import pattern that breaks cell mode)
      var bareName = _stripMethodNameSuffix(m.name);
      if (EXCLUDED_METHOD_NAMES.indexOf(bareName) !== -1) continue;
      filtered.push(m);
    }
    if (filtered.length === 0) continue;
    var section = "## " + tool.programName;
    if (tool.description) section += "\n" + tool.description;
    section += "\n\n";
    for (var fi = 0; fi < filtered.length; fi++) {
      section += filtered[fi].content + "\n\n";
    }
    sections.push(section);
  }
  return sections.join("\n---\n\n");
}

// ============================================================================
// Available functions / variables rendering
// ============================================================================

function renderAvailableFns(availableFns, cumulativeTrace) {
  var names = Object.keys(availableFns);
  if (names.length === 0) return "(none yet — define your first helper to populate)";
  var lines = [];
  for (var i = 0; i < names.length; i++) {
    var name = names[i];
    var info = availableFns[name];
    var entry = "- " + name + (info.signature || "()");
    if (info.description) entry += "\n  " + info.description;
    var mock = mostRecentTraceEntry(cumulativeTrace, name);
    if (mock) {
      var inSummary = mock.input;
      try {
        var parsed = JSON.parse(mock.input);
        if (Array.isArray(parsed)) {
          var argParts = [];
          for (var ai = 0; ai < parsed.length; ai++) argParts.push(inferSchema(parsed[ai]));
          inSummary = "(" + argParts.join(", ") + ")";
        }
      } catch (e) {}
      entry += "\n  sample: " + name + inSummary + " → " + inferSchema(mock.output);
    } else {
      entry += "\n  (no test call yet — call to populate sample)";
    }
    lines.push(entry);
  }
  return lines.join("\n\n");
}

function renderAvailableVars(availableVars, varValues) {
  var names = Object.keys(availableVars);
  if (names.length === 0) return "(none — write a // comment above a var to register it)";
  var lines = [];
  for (var i = 0; i < names.length; i++) {
    var name = names[i];
    var info = availableVars[name];
    var entry = "- " + name;
    if (info.description) entry += "\n  " + info.description;
    var v = varValues[name];
    if (v === null || v === undefined) {
      entry += "\n  value: (undefined — variable was removed)";
    } else {
      entry += "\n  value: " + v;
    }
    lines.push(entry);
  }
  return lines.join("\n\n");
}

// Rolling detail: last DETAIL_WINDOW entries get the full code block; older
// entries get thought + traceOneLiner + lastValue summary only. Code is the
// biggest per-turn cost; old code blocks are dropped from rendering, but
// thoughts and observations are kept for the full history (no truncation).
var DETAIL_WINDOW = 5;

function renderStepLog(stepLog) {
  if (stepLog.length === 0) return "(no turns yet)";
  var entries = [];
  var oldestDetailedIdx = Math.max(0, stepLog.length - DETAIL_WINDOW);
  for (var i = 0; i < stepLog.length; i++) {
    var s = stepLog[i];
    var detailed = i >= oldestDetailedIdx;
    var entry = "### Turn " + s.turn + " [" + s.phase + "] (" + s.action + ")";
    if (s.thought) entry += "\nThought: " + s.thought;
    if (detailed && s.code) {
      entry += "\nCode:\n```javascript\n" + s.code + "\n```";
    }
    if (s.traceOneLiner) entry += "\nEffects:\n" + s.traceOneLiner;
    if (s.lastValue !== null && s.lastValue !== undefined && s.lastValue !== "undefined") {
      entry += "\nLast value: " + s.lastValue;
    }
    if (s.error) entry += "\nError: " + s.error;
    entries.push(entry);
  }
  return entries.join("\n\n");
}

// ============================================================================
// HARDCODED PROMPTS
// ============================================================================

var SYSTEM = "" +
"You are a code-synthesis agent. You incrementally build a small program from named JavaScript functions. " +
"Each function is a tested, traced unit. The final program composes those functions to satisfy the user's request.\n\n" +
"You operate in three advisory phases:\n" +
"- explore: probe the available data with throwaway named functions; learn shapes\n" +
"- build:   define getter and transformer helpers as named functions; test each inline\n" +
"- apply:   call your registered helpers from a final orchestration step\n\n" +
"Declare your current phase in the structured output. Phase transitions are your call — there is no enforcement.";

var KERNEL_API = "" +
"## Kernel API (persistent JS cell — Jupyter-style)\n\n" +
"Cell semantics, NOT a function. DO NOT write `function main(args) {...}` or `return ...`.\n" +
"DO NOT write `import` statements (Sobek RunString doesn't accept them).\n" +
"Write top-level statements that build on prior turns.\n\n" +
"- `client` is already bound for you (the Anytype helper client). Just use `client.X()`.\n" +
"- `inferSchema` is already bound for you. Use it for ad-hoc shape inspection.\n" +
"- `var x = ...` at top level binds the name and makes it discoverable to the agent loop.\n" +
"  Use `var` for any function or variable you want surfaced in 'Global functions' / 'Global variables' next turn.\n" +
"- `let` / `const` work but are NOT discovered by the agent loop. Use them for loop counters and block locals.\n" +
"- To finish: call `done(value)` — terminates with `value` as the answer. First call wins.\n" +
"- To ask the user: call `ask(\"question\")` — pauses the run.\n" +
"- To wipe kernel state mid-request: `js.reset()` then re-bootstrap.\n" +
"- Bare expressions at end of cell (e.g., `docs.length`) are captured as debug observations but do NOT terminate the goal.\n" +
"- To import another module (rare — `client` covers Anytype): `var foo = __import(\"foo@v1\");` then use `foo.method()`.\n\n" +
"When you define a helper function:\n" +
"- ALWAYS wrap operations in a named function. Do not call API methods inline.\n" +
"- Above each function, write a `// short description` (1-3 lines).\n" +
"- On the next line after the definition, test the function with sample args.\n" +
"  The runtime auto-wraps top-level functions and records the test call as the function's mock.\n\n" +
"When you define a persistent variable:\n" +
"- Above each `var name = ...` whose value should be available in future turns,\n" +
"  write a `// short description` of what it holds and why.\n" +
"- Variables without a description still bind to the kernel but are not surfaced.\n\n" +
"Observation discipline:\n" +
"- DO NOT JSON.stringify or console.log full API response bodies. They're already bound to your variable.\n" +
"- Effect calls (anyHelper, your helpers) are recorded in the trace and shown next turn as schema-only one-liners.\n" +
"- Reference prior bindings by name. They're live in the kernel.";

var PHASE_GUIDANCE = "" +
"## Phase examples\n\n" +
"### explore\n" +
"```\n" +
"// List available types\n" +
"var probe_types = function() { return client.getTypes(); };\n" +
"probe_types();\n" +
"```\n\n" +
"### build\n" +
"```\n" +
"// Get most recent reading-list notes, optionally limited\n" +
"var get_recent_notes = function(limit) {\n" +
"  return client.getObjects(\"any_note\").records.slice(0, limit || 10);\n" +
"};\n" +
"get_recent_notes(3);  // test with sample\n" +
"```\n\n" +
"### apply\n" +
"```\n" +
"var grouped = group_by_month(get_recent_notes(50));\n" +
"done(\"Found \" + Object.keys(grouped).length + \" months of notes\");\n" +
"```";

var OUTPUT_FORMAT = "" +
"## Output format (strict)\n\n" +
"Output a single JSON object on its own:\n" +
"{\n" +
"  \"thought\": \"<one or two sentences explaining what this turn does>\",\n" +
"  \"phase\":   \"explore\" | \"build\" | \"apply\",\n" +
"  \"code\":    \"<JavaScript cell source>\"\n" +
"}\n\n" +
"No markdown fences around the JSON. The `code` field is a JS string with newlines escaped as \\n.";

// ============================================================================
// Prompt builder
// ============================================================================

function buildPrompt(state, allMethods, userText) {
  // Filter tool docs by phase. Only `explore` is read-only — `build` needs
  // mutators because that's where helpers wrapping createX/updateX get
  // defined and tested. `apply` and any unknown phase default to all tools
  // (defensive: better to over-include than to silently hide methods the
  // model needs).
  var allowedKinds = (state.phase === "explore")
    ? ["getter", "setup"]
    : ["getter", "setup", "mutator"];
  var toolDocs = renderToolDocs(allMethods, allowedKinds);

  // Render available functions and variables
  var fnSection = renderAvailableFns(state.availableFns, state.cumulativeTrace);
  var varValues = probeVariableValues(Object.keys(state.availableVars));
  var varSection = renderAvailableVars(state.availableVars, varValues);

  // Render full step log (no truncation, no rolling cap)
  var logSection = renderStepLog(state.stepLog);

  return SYSTEM + "\n\n"
       + KERNEL_API + "\n\n"
       + PHASE_GUIDANCE + "\n\n"
       + OUTPUT_FORMAT + "\n\n"
       + "## Available tools (current phase: " + state.phase + ")\n\n" + toolDocs + "\n\n"
       + "## Global functions you have built\n\n" + fnSection + "\n\n"
       + "## Global variables (persistent)\n\n" + varSection + "\n\n"
       + "## Step log\n\n" + logSection + "\n\n"
       + "## Current phase: " + state.phase + "\n\n"
       + "## User request\n" + userText + "\n";
}

// ============================================================================
// MAIN LOOP
// ============================================================================

export function main(args) {
  if (!args) args = {};
  if (!args.apiBaseUrl) args.apiBaseUrl = env.ANYTYPE_API_URL;
  if (!args.apiKey)     args.apiKey     = env.ANYTYPE_API_KEY;
  if (!args.spaceId)    args.spaceId    = env.ANYTYPE_SPACE_ID;
  if (!args.text)       return "Error: args.text required";

  var client = createClient({
    apiBaseUrl: args.apiBaseUrl,
    apiKey: args.apiKey,
    spaceId: args.spaceId
  });

  // Tool discovery — same shape as v4, now with method.kind
  var tools = client.getTools();
  if (!tools || tools.length === 0) {
    return "Error: no tools available in space";
  }
  var allMethods = {};
  for (var ti = 0; ti < tools.length; ti++) {
    var ext = extractToolMethods(client, tools[ti].id);
    allMethods[tools[ti].id] = {
      programName: tools[ti].programName,
      methods: ext.methods,
      description: ext.description
    };
  }

  // Reset persistent kernel — clean slate per request
  js.reset();

  // Bootstrap: bind client and inferSchema as persistent globals.
  // Note: Sobek's RunString doesn't accept ES `import` statements; the runtime
  // exposes a `__import("specifier")` host helper that returns the namespace
  // object of a resolved module. Use that instead.
  var bootstrap =
    'var __helper = __import("anyHelper@v1");\n' +
    'var createClient = __helper.createClient;\n' +
    'var client = createClient(args);\n' +
    'var __ns_utils = __import("utils@v1");\n' +
    'var inferSchema = __ns_utils.inferSchema;';
  var bootResult = js.eval(bootstrap, args, { persistent: true });
  if (bootResult && bootResult.error) {
    return "Bootstrap failed: " + bootResult.error;
  }

  // Snapshot baseline globals (after bootstrap, so client + inferSchema are baseline)
  var baselineGlobals = snapshotGlobals();

  // State
  var state = {
    phase: "explore",
    availableFns: {},      // name → {signature, description, definedTurn}
    availableVars: {},     // name → {description, definedTurn}
    cumulativeTrace: {},
    stepLog: [],
    baselineGlobals: baselineGlobals,
    prevGlobals: baselineGlobals.slice()
  };

  for (var iter = 0; iter < MAX_ITERS; iter++) {
    var prompt = buildPrompt(state, allMethods, args.text);

    var resp;
    try {
      resp = llm.codegen(prompt);
    } catch (e) {
      chatReply("LLM error on turn " + (iter + 1) + ": " + (e.message || e));
      return "FAILED at turn " + (iter + 1) + ": LLM error";
    }
    if (!resp) {
      chatReply("Empty LLM response on turn " + (iter + 1));
      return "FAILED at turn " + (iter + 1) + ": empty LLM response";
    }

    var parsed = parseStructured(resp);
    if (parsed.phase) state.phase = parsed.phase;
    if (!parsed.code || parsed.code.trim() === "") {
      // No code — record turn and continue (or break if many in a row)
      state.stepLog.push({
        turn: iter + 1,
        phase: state.phase,
        thought: parsed.thought,
        traceOneLiner: "",
        action: "no-code",
        error: ""
      });
      continue;
    }

    chatReply("Turn " + (iter + 1) + " [" + state.phase + "]: " + (parsed.thought || "(no thought)"));

    // Snapshot before exec — but use prevGlobals from last turn for diff
    var beforeGlobals = state.prevGlobals;

    var result = js.eval(parsed.code, args, { persistent: true, enableWrapMethods: true });

    // Detect new top-level globals
    var afterGlobals = snapshotGlobals();
    state.prevGlobals = afterGlobals;
    var newNames = [];
    for (var ai = 0; ai < afterGlobals.length; ai++) {
      if (beforeGlobals.indexOf(afterGlobals[ai]) === -1) newNames.push(afterGlobals[ai]);
    }

    // Classify each new name as function vs variable
    for (var ni = 0; ni < newNames.length; ni++) {
      var name = newNames[ni];
      var description = extractDescriptionAbove(parsed.code, name);
      var typeProbe = js.eval("typeof " + name, {}, { persistent: true });
      var typeStr = (typeProbe && typeProbe.lastValue) || (typeProbe && typeProbe.result) || "undefined";
      if (typeStr === "function") {
        var signature = extractSignature(parsed.code, name);
        state.availableFns[name] = {
          signature: signature,
          description: description,
          definedTurn: iter + 1
        };
      } else {
        // Variables register only if model wrote a description
        if (description) {
          state.availableVars[name] = {
            description: description,
            definedTurn: iter + 1
          };
        }
      }
    }

    // Merge per-turn trace into cumulative
    mergeTraces(state.cumulativeTrace, result.traces || {});

    // Error handling — call existing tracer if a runtime/api error occurred
    var detected = detectError(result);
    if (detected.hasError && (detected.errorType === "runtime" || detected.errorType === "api_error")) {
      try {
        // Tracer gets the FULL trace (not codegen-filtered)
        var allToolDocs = renderToolDocs(allMethods, ["getter", "setup", "mutator"]);
        var fixed = runTracer({
          code: parsed.code,
          args: args,
          error: detected.message,
          result: result.result,
          traces: result.traces,
          toolDocs: allToolDocs,
          stepTitle: args.text
        });
        if (fixed && fixed.fixed) {
          // Re-eval the fixed code through the persistent kernel
          var refixed = js.eval(fixed.code, args, { persistent: true, enableWrapMethods: true });
          if (refixed && !refixed.error) {
            result = refixed;
            mergeTraces(state.cumulativeTrace, result.traces || {});
            // Re-snapshot after fix
            state.prevGlobals = snapshotGlobals();
          }
        }
      } catch (tracerErr) {
        // Tracer crashed — leave the original error in result, log nothing
      }
    }

    // Step log entry (filtered trace for prompt readability)
    var visibleTraces = filterTraceForCodegen(result.traces || {});
    state.stepLog.push({
      turn: iter + 1,
      phase: state.phase,
      thought: parsed.thought || "",
      traceOneLiner: formatTraceOneLiner(visibleTraces),
      action: result.action || "continue",
      error: result.error || ""
    });

    // Termination check
    if (result.action === "done") {
      var ans = result.result;
      var finalText = typeof ans === "string" ? ans : JSON.stringify(ans);
      chatReply(finalText);
      return finalText;
    }
    if (result.action === "ask") {
      var question = typeof result.result === "string" ? result.result : JSON.stringify(result.result);
      chatReply("ASK: " + question);
      return "ASK: " + question;
    }
  }

  return "MAX_ITERS exhausted (" + MAX_ITERS + ")";
}
