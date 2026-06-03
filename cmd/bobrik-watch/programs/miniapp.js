import { createClient, editString } from "anyHelper@v1";

// miniapp — author and manage embeddable HTML/JS mini apps.
//
// Storage: each mini app is one object of the built-in `Mini App` type whose
// source / state / readme live in the structured `mini_app` dataset (record
// "main"), NOT in markdown blocks. The object's display name (any.name) IS the
// app's unique name. This replaces the old anytypeHelper scheme that wrote a
// "## Source"/"## State" markdown body and parsed it back with fragile fence
// matching — source/state now round-trip byte-for-byte and update atomically
// per-field (editing state never rewrites source).

var TYPE = "mini_app";  // type xKey (== builtin id); display name "Mini App"
var DATASET = "mini_app";
var RECORD = "main";

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

// ------------------------------------------------------------------
// Internals
// ------------------------------------------------------------------

// Find a mini-app object by its name (any.name). Returns the object record or
// null. listing uses the cross-object query (one call), no per-object reads.
function _findByName(name) {
  var all = _c().getObjects(TYPE).records;
  for (var i = 0; i < all.length; i++) {
    if (all[i].name === name) return all[i];
  }
  return null;
}

// Load the mini_app dataset record for an object id. Returns { source, state,
// readme } with string defaults, or null if the object/record is missing.
function _loadParts(objId) {
  var res = _c().getObjects({ objectId: objId, dataset: DATASET });
  var rec = null;
  if (res && res.ok) {
    for (var i = 0; i < res.records.length; i++) {
      if (res.records[i].id === RECORD) { rec = res.records[i]; break; }
    }
    if (!rec && res.records.length) rec = res.records[0];
  }
  if (!rec) return { source: "", state: null, readme: "" };
  return {
    source: typeof rec.source === "string" ? rec.source : "",
    state: rec.state != null ? rec.state : null,
    readme: typeof rec.readme === "string" ? rec.readme : ""
  };
}

// Runtime-script guard — prepend React UMD tags + the useAnytypeState hook
// script if missing. All three are loaded by src from the embed's base path
// (./react.js, ./react-dom.js, ./useAnytypeState.js) so mini-app source files
// stay clean — no inline hook definition bloating every file. Scripts must
// load BEFORE the author's inline <script>.
function _ensureRuntimeScripts(html) {
  var hasReact    = /<script[^>]*src=["']\.\/react\.js["']/.test(html);
  var hasReactDom = /<script[^>]*src=["']\.\/react-dom\.js["']/.test(html);
  var hasHook     = /<script[^>]*src=["']\.\/useAnytypeState\.js["']/.test(html);
  if (hasReact && hasReactDom && hasHook) return { html: html, injected: null };

  var inject = "";
  var injected = [];
  if (!hasReact)    { inject += '<script src="./react.js"></script>\n';           injected.push("react.js"); }
  if (!hasReactDom) { inject += '<script src="./react-dom.js"></script>\n';       injected.push("react-dom.js"); }
  if (!hasHook)     { inject += '<script src="./useAnytypeState.js"></script>\n'; injected.push("useAnytypeState.js"); }
  return { html: inject + html, injected: injected };
}

function _serializeState(state) {
  if (state == null) return { ok: true, text: null };
  if (typeof state === "string") return { ok: true, text: state };
  try {
    return { ok: true, text: JSON.stringify(state, null, 2) };
  } catch (e) {
    return { ok: false, error: "state is not JSON-serializable: " + String(e) };
  }
}

function _sliceLines(text, from, to) {
  var lines = (text || "").split("\n");
  var total = lines.length;
  var f = from != null ? Math.max(1, from) : 1;
  var t = to   != null ? Math.min(total, to) : total;
  if (f > t) f = t;
  return { text: lines.slice(f - 1, t).join("\n"), range: { from: f, to: t, totalLines: total } };
}

// ------------------------------------------------------------------
// Public API
// ------------------------------------------------------------------

export function createMiniApp(opts) {
  if (!opts || typeof opts !== "object") return { ok: false, error: "opts object required" };
  var name = opts.name;
  if (!name) return { ok: false, error: "opts.name is required" };
  if (!opts.source) return { ok: false, error: "opts.source is required (full HTML)" };

  if (_findByName(name)) {
    return { ok: false, error: "mini app '" + name + "' already exists. Use updateMiniApp to modify." };
  }

  var guard = _ensureRuntimeScripts(opts.source);
  var st = _serializeState(opts.state);
  if (!st.ok) return { ok: false, error: st.error };

  var res = _c().createObject(TYPE, { name: name });
  if (!res.ok) return { ok: false, error: res.error };
  var id = res.object && res.object.id;

  var fields = { source: guard.html, readme: opts.readme || "" };
  if (st.text != null) fields.state = st.text;
  var wr = _c().setRecord(id, DATASET, RECORD, fields);
  if (!wr.ok) return { ok: false, id: id, error: "object created but content write failed: " + wr.error };

  var out = { ok: true, name: name, title: name, object: res.object, id: id };
  if (guard.injected) out.warnings = ["auto-injected missing runtime script(s): " + guard.injected.join(", ")];
  return out;
}

export function updateMiniApp(opts) {
  if (!opts || typeof opts !== "object") return { ok: false, error: "opts object required" };
  var name = opts.name;
  if (!name) return { ok: false, error: "opts.name is required" };

  var obj = _findByName(name);
  if (!obj) return { ok: false, error: "mini app '" + name + "' not found" };

  var fields = {};
  var warnings = [];

  if (opts.source != null) {
    var guard = _ensureRuntimeScripts(opts.source);
    fields.source = guard.html;
    if (guard.injected) warnings.push("auto-injected missing runtime script(s): " + guard.injected.join(", "));
  }
  if (opts.state != null) {
    var st = _serializeState(opts.state);
    if (!st.ok) return { ok: false, name: name, error: st.error };
    fields.state = st.text;
  }
  if (opts.readme != null) fields.readme = opts.readme;

  if (Object.keys(fields).length > 0) {
    var wr = _c().setRecord(obj.id, DATASET, RECORD, fields);
    if (!wr.ok) return { ok: false, name: name, error: wr.error };
  }
  if (opts.title) _c().updateObject(obj.id, { name: opts.title });

  var out = { ok: true, name: name, object: { id: obj.id } };
  if (warnings.length > 0) out.warnings = warnings;
  return out;
}

export function editMiniApp(name, opts) {
  if (!name) return { ok: false, error: "name is required" };
  if (!opts || typeof opts !== "object") {
    return { ok: false, name: name, error: "opts object required with oldString, newString, block?, replaceAll?" };
  }
  var block = opts.block || "source";
  if (block !== "source" && block !== "state") {
    return { ok: false, name: name, error: "opts.block must be 'source' or 'state'" };
  }

  // snake_case aliases — agents reach for these from muscle memory.
  var oldString  = opts.oldString  != null ? opts.oldString  : opts.old_str;
  var newString  = opts.newString  != null ? opts.newString  : opts.new_str;
  var replaceAll = opts.replaceAll != null ? opts.replaceAll : opts.replace_all;

  var obj = _findByName(name);
  if (!obj) return { ok: false, name: name, error: "mini app '" + name + "' not found" };

  var parts = _loadParts(obj.id);
  var target = block === "source" ? parts.source : parts.state;
  if (target == null || target === "") {
    return { ok: false, name: name, block: block, error: "block '" + block + "' is empty or missing" };
  }

  var r = editString(target, oldString, newString, replaceAll);
  if (!r.ok) return { ok: false, name: name, block: block, error: r.error, lengthBefore: target.length };

  var newBlock = r.result;
  if (block === "source") newBlock = _ensureRuntimeScripts(newBlock).html;

  var fields = {};
  fields[block] = newBlock;
  var wr = _c().setRecord(obj.id, DATASET, RECORD, fields);
  if (!wr.ok) return { ok: false, name: name, error: wr.error };

  return {
    ok: true, name: name, block: block,
    replacements: r.replacements,
    lengthBefore: target.length, lengthAfter: newBlock.length,
    object: { id: obj.id }
  };
}

export function setState(name, stateObject) {
  if (!name) return { ok: false, error: "name is required" };
  if (stateObject === undefined) return { ok: false, name: name, error: "stateObject is required" };
  var st = _serializeState(stateObject);
  if (!st.ok) return { ok: false, name: name, error: st.error };

  var obj = _findByName(name);
  if (!obj) return { ok: false, name: name, error: "mini app '" + name + "' not found" };

  var wr = _c().setRecord(obj.id, DATASET, RECORD, { state: st.text });
  if (!wr.ok) return { ok: false, name: name, error: wr.error };
  return { ok: true, name: name, object: { id: obj.id } };
}

export function getState(name) {
  if (!name) return null;
  var obj = _findByName(name);
  if (!obj) return null;
  var parts = _loadParts(obj.id);
  if (parts.state == null) return null;
  try { return JSON.parse(parts.state); }
  catch (e) {
    console.log("getState: JSON parse failed for '" + name + "': " + String(e));
    return null;
  }
}

export function upsertReadme(name, newReadme) {
  if (!name) return { ok: false, error: "name is required" };
  if (typeof newReadme !== "string") {
    return { ok: false, name: name, error: "newReadme must be a string" };
  }
  var obj = _findByName(name);
  if (!obj) return { ok: false, name: name, error: "mini app '" + name + "' not found" };
  var wr = _c().setRecord(obj.id, DATASET, RECORD, { readme: newReadme });
  if (!wr.ok) return { ok: false, name: name, error: wr.error };
  return { ok: true, name: name, object: { id: obj.id } };
}

export function upsertMiniAppState(name, source) {
  if (!name) return { ok: false, error: "name is required" };
  if (typeof source !== "string") {
    return { ok: false, name: name, error: "source must be a string (raw JSON text)" };
  }
  var obj = _findByName(name);
  if (!obj) return { ok: false, name: name, error: "mini app '" + name + "' not found" };
  var existed = _loadParts(obj.id).state != null;
  var wr = _c().setRecord(obj.id, DATASET, RECORD, { state: source });
  if (!wr.ok) return { ok: false, name: name, error: wr.error };
  return { ok: true, name: name, created: !existed, object: { id: obj.id } };
}

export function getMiniApp(name, opts) {
  if (!name) return null;
  var obj = _findByName(name);
  if (!obj) return null;
  var parts = _loadParts(obj.id);
  var state = null;
  if (parts.state != null) {
    try { state = JSON.parse(parts.state); } catch (e) { state = parts.state; }
  }
  var source = parts.source;
  var out = {
    id: obj.id,
    name: name,
    title: obj.name,
    source: source,
    state: state,
    readme: parts.readme
  };
  if (opts && (opts.from != null || opts.to != null)) {
    var slice = _sliceLines(source, opts.from, opts.to);
    out.source = slice.text;
    out.range = slice.range;
  }
  return out;
}

export function getMiniAppSource(name, opts) {
  if (!name) return null;
  var obj = _findByName(name);
  if (!obj) return null;
  var parts = _loadParts(obj.id);
  if (opts && (opts.from != null || opts.to != null)) {
    var slice = _sliceLines(parts.source, opts.from, opts.to);
    return { name: name, source: slice.text, range: slice.range };
  }
  return { name: name, source: parts.source };
}

export function listMiniApps() {
  var all = _c().getObjects(TYPE).records;
  var out = [];
  for (var i = 0; i < all.length; i++) {
    if (!all[i].name) continue;
    out.push({ id: all[i].id, name: all[i].name, title: all[i].name });
  }
  out.sort(function(a, b) { return a.name < b.name ? -1 : a.name > b.name ? 1 : 0; });
  return out;
}

export function main(args) {
  args = args || {};
  if (args.name) {
    var app = getMiniApp(args.name);
    return app ? JSON.stringify(app) : "mini app not found: " + args.name;
  }
  return JSON.stringify(listMiniApps());
}
