// __main_source
// utils@v1 — pure utility functions used by toolcall_core.
// No client, no fetch, no LLM. Self-contained so importers can pull these
// without dragging in any other module's dependency chain.
//
// Exports:
//   inferSchema(val)             — shape/preview string; full containers,
//                                  big strings get 200-char head + remaining
//   displayValue(val)            — "Last value:" formatting; full containers,
//                                  strings never truncated at any depth
//   formatTraceOneLiner(traces)  — one-line summary per traced effect call
//   summarizeTrace(traces)       — counts + first/last samples for big traces
//
// Both walkers are non-truncating at the container level; they only differ in
// how they render strings. The caller that produced the value (Effects / the
// cell itself) already has it somewhere in context — we show the full shape
// without collapsing "+N more" or "N items", so no prop is hidden.
//
// Kernel callers can pull these via __import("utils@v1") at bootstrap; no
// source-string duplication needed.

// Two string rendering strategies. The recursive walker takes one as a
// parameter; strings encountered at any depth go through the chosen strategy.
function _renderFull(s) {
  // Never truncate. Used by displayValue so the model gets every char the cell
  // produced, even if the string is nested inside an array/object.
  return JSON.stringify(s);
}
function _renderPreview(s) {
  // 200-char head + remaining-size suffix. Used by inferSchema so big strings
  // nested in a returned object still show their head instead of a raw count.
  if (s.length <= 200) return JSON.stringify(s);
  return JSON.stringify(s.substring(0, 200)) + " ... string(" + (s.length - 200) + ")";
}

// _walkFull — walks a value with no container truncation: every array element
// and every object key is rendered. Only strings pass through the chosen
// renderString strategy (preview or full).
function _walkFull(val, renderString) {
  if (val === null) return "null";
  if (val === undefined) return "undefined";
  if (typeof val === "boolean") return String(val);
  if (typeof val === "number") return String(val);
  if (typeof val === "string") return renderString(val);

  if (Array.isArray(val)) {
    if (val.length === 0) return "[]";
    var parts = [];
    for (var i = 0; i < val.length; i++) parts.push(_walkFull(val[i], renderString));
    return "[" + parts.join(", ") + "]";
  }

  if (typeof val === "object") {
    var keys = Object.keys(val);
    if (keys.length === 0) return "{}";
    var pairs = [];
    for (var ki = 0; ki < keys.length; ki++) {
      pairs.push(keys[ki] + ": " + _walkFull(val[keys[ki]], renderString));
    }
    return "{" + pairs.join(", ") + "}";
  }

  return String(typeof val);
}

export function inferSchema(val) {
  return _walkFull(val, _renderPreview);
}

// displayValue — for "Last value:" in tool_result. Strings never truncated at
// any depth; containers shown in full. The cell's final expression IS what the
// model chose to surface — echo it back intact.
export function displayValue(val) {
  return _walkFull(val, _renderFull);
}

// ============================================================================
// formatTraceOneLiner — one-line summary per visible effect call
// ============================================================================
// Each effect entry: "name(input) → schema-summarized output"

export function formatTraceOneLiner(traces) {
  if (!traces) return "";
  var lines = [];
  for (var name in traces) {
    var record = traces[name];
    for (var input in record) {
      var outputs = record[input];
      var lastOut = outputs[outputs.length - 1];
      var inSummary = input;
      try {
        var parsed = JSON.parse(input);
        if (Array.isArray(parsed)) {
          var argParts = [];
          for (var ai = 0; ai < parsed.length; ai++) {
            argParts.push(inferSchema(parsed[ai]));
          }
          inSummary = "(" + argParts.join(", ") + ")";
        }
      } catch (e) {
        if (inSummary.length > 80) inSummary = inSummary.substring(0, 80) + "...";
      }
      // Outputs are stored JSON-stringified by the runtime's trace layer.
      // Parse them back so the one-liner shows structural schema
      // (e.g. "list[20] of {...}") instead of an opaque "string(22653)".
      var outValue = lastOut;
      if (typeof lastOut === "string") {
        try { outValue = JSON.parse(lastOut); } catch (e) {}
      }
      var outSummary = inferSchema(outValue);
      lines.push(name + inSummary + " → " + outSummary);
    }
  }
  return lines.join("\n");
}

// ============================================================================
// summarizeTrace — counts + first/last samples for large traces
// ============================================================================
// When a cell's full trace one-liner would push tool_result over the harness
// budget, the harness swaps it for this digest and stashes the full one-liner
// array in toolEffects. Groups by (name + arg-types + coarse-out schema) so
// 200 getObject calls that all return Accident objects collapse into one row.

function _argType(v) {
  if (v === null) return "null";
  if (Array.isArray(v)) return "array";
  if (typeof v === "object") return "obj";
  return typeof v;
}

function _coarseLeafType(v) {
  if (v === null) return "null";
  if (Array.isArray(v)) return "list";
  if (typeof v === "object") return "obj";
  return typeof v;
}

function _coarseOutSchema(v) {
  if (v === null) return "null";
  if (v === undefined) return "undefined";
  if (typeof v !== "object") return typeof v;
  if (Array.isArray(v)) {
    if (v.length === 0) return "[]";
    return "list[" + v.length + "]";
  }
  var keys = Object.keys(v);
  if (keys.length === 0) return "{}";
  if (keys.length <= 5) {
    var pairs = [];
    for (var i = 0; i < keys.length; i++) {
      pairs.push(keys[i] + ":" + _coarseLeafType(v[keys[i]]));
    }
    return "{" + pairs.join(",") + "}";
  }
  return "object";
}

function _maybeParseJson(v) {
  if (typeof v === "string") {
    try { return JSON.parse(v); } catch (e) {}
  }
  return v;
}

function _argTypesFromInputKey(inputKey) {
  try {
    var parsed = JSON.parse(inputKey);
    if (Array.isArray(parsed)) {
      var types = [];
      for (var i = 0; i < parsed.length; i++) types.push(_argType(parsed[i]));
      return types.join(",");
    }
  } catch (e) {}
  return "...";
}

function _renderInputKey(inputKey) {
  try {
    var parsed = JSON.parse(inputKey);
    if (Array.isArray(parsed)) {
      var argParts = [];
      for (var i = 0; i < parsed.length; i++) argParts.push(inferSchema(parsed[i]));
      return "(" + argParts.join(", ") + ")";
    }
  } catch (e) {}
  if (inputKey.length > 80) return "(" + inputKey.substring(0, 80) + "...)";
  return "(" + inputKey + ")";
}

export function summarizeTrace(traces) {
  if (!traces) return null;
  var nameOrder = [];
  var perName = {};
  var totalCalls = 0;
  var firstSample = null;
  var lastSample = null;

  for (var name in traces) {
    if (!traces.hasOwnProperty(name)) continue;
    nameOrder.push(name);
    perName[name] = { sigCounts: {}, sigOrder: [] };
    var record = traces[name];
    for (var input in record) {
      if (!record.hasOwnProperty(input)) continue;
      var outputs = record[input];
      if (!outputs || outputs.length === 0) continue;

      var argTypes = _argTypesFromInputKey(input);
      var lastOutVal = _maybeParseJson(outputs[outputs.length - 1]);
      var sig = name + "(" + argTypes + ") → " + _coarseOutSchema(lastOutVal);

      if (!perName[name].sigCounts[sig]) {
        perName[name].sigCounts[sig] = 0;
        perName[name].sigOrder.push(sig);
      }
      perName[name].sigCounts[sig] += outputs.length;
      totalCalls += outputs.length;

      var firstOutVal = _maybeParseJson(outputs[0]);
      var argLine = _renderInputKey(input);
      if (firstSample === null) {
        firstSample = name + argLine + " → " + inferSchema(firstOutVal);
      }
      lastSample = name + argLine + " → " + inferSchema(lastOutVal);
    }
  }

  if (totalCalls === 0) return null;

  var groupLines = [];
  for (var ni = 0; ni < nameOrder.length; ni++) {
    var nm = nameOrder[ni];
    var sigs = perName[nm].sigOrder;
    for (var si = 0; si < sigs.length; si++) {
      groupLines.push("  " + sigs[si] + "  ×" + perName[nm].sigCounts[sigs[si]]);
    }
  }
  return { totalCalls: totalCalls, groupLines: groupLines, firstLine: firstSample, lastLine: lastSample };
}

export function main() {
  return "utils@v1 — exports inferSchema, displayValue, formatTraceOneLiner, summarizeTrace";
}
