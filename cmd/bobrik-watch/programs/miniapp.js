import { createClient, editString } from "anyHelper@v1";

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

function _miniappName(obj) {
  if (!obj) return null;
  return obj.miniapp_name || (obj.properties && obj.properties.miniapp_name) || null;
}

function _findByName(name) {
  var all = _c().getObjects("anytype_mini_app");
  for (var i = 0; i < all.length; i++) {
    if (_miniappName(all[i]) === name) return all[i];
  }
  return null;
}

function _loadFull(name) {
  var lite = _findByName(name);
  if (!lite) return null;
  return _c().getObject(lite.id);
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

// Build full body from parts.
function _buildBody(parts) {
  var readme = (parts.readme || "").replace(/\s+$/, "");
  var out = readme;
  if (out.length > 0) out += "\n\n";
  out += "## Source\n\n```html\n" + (parts.source || "") + "\n```";
  if (parts.state != null) {
    out += "\n\n## State\n\n```json\n" + parts.state + "\n```";
  }
  return out;
}

// Locate the first code block that follows a given heading. The Anytype API
// strips lang tags from fences on round-trip (bare ``` comes back), so we
// anchor on the heading and search for the next pair of fences. Returns
// { content, openIdx, closeEnd } or null. openIdx points at the opening fence's
// first backtick; closeEnd is one past the closing fence's last backtick.
function _findBlockSpan(md, heading) {
  var hIdx = md.indexOf(heading);
  if (hIdx === -1) return null;
  var openIdx = md.indexOf("```", hIdx + heading.length);
  if (openIdx === -1) return null;
  // Skip past "```" + optional lang tag + newline to find content start.
  var contentStart = md.indexOf("\n", openIdx + 3);
  if (contentStart === -1) return null;
  contentStart += 1;
  var closeIdx = md.indexOf("```", contentStart);
  if (closeIdx === -1) return null;
  // Strip the trailing "\n" before the closing fence (we wrote it that way).
  var content = md.substring(contentStart, closeIdx);
  if (content.length > 0 && content.charAt(content.length - 1) === "\n") {
    content = content.substring(0, content.length - 1);
  }
  return { content: content, openIdx: openIdx, closeEnd: closeIdx + 3 };
}

// Extract the three parts from an existing body. Returns
// { readme, source, state }. `state` is null if no state block exists.
function _parseBody(md) {
  md = md || "";
  var sourceSpan = _findBlockSpan(md, "## Source");
  var stateSpan  = _findBlockSpan(md, "## State");

  var cutIdx = md.indexOf("## Source");
  if (cutIdx === -1 && sourceSpan) cutIdx = sourceSpan.openIdx;
  if (cutIdx === -1) cutIdx = md.length;
  // The API often prepends "\n" to bodies — trim both ends of the readme.
  var readme = md.substring(0, cutIdx).replace(/^\s+|\s+$/g, "");

  return {
    readme: readme,
    source: sourceSpan ? sourceSpan.content : "",
    state:  stateSpan  ? stateSpan.content  : null
  };
}

// Replace a block's content in-place. Appends a fresh section if absent.
// blockType: "source" | "state"
function _replaceBlock(md, blockType, newContent) {
  md = md || "";
  var heading = blockType === "source" ? "## Source" : "## State";
  var fence   = blockType === "source" ? "html" : "json";
  var span = _findBlockSpan(md, heading);
  if (span) {
    var newFence = "```" + fence + "\n" + newContent + "\n```";
    return md.substring(0, span.openIdx) + newFence + md.substring(span.closeEnd);
  }
  var joiner = md.length > 0 ? "\n\n" : "";
  return md + joiner + heading + "\n\n```" + fence + "\n" + newContent + "\n```";
}

// Replace everything before the "## Source" heading with newReadme.
function _replaceReadme(md, newReadme) {
  md = md || "";
  var cutIdx = md.indexOf("## Source");
  var readme = (newReadme || "").replace(/\s+$/, "");
  if (cutIdx === -1) return readme;
  var rest = md.substring(cutIdx);
  var joiner = readme.length > 0 ? "\n\n" : "";
  return readme + joiner + rest;
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

  var body = _buildBody({ readme: opts.readme, source: guard.html, state: st.text });
  var title = opts.title || name;

  var res = _c().createObject("anytype_mini_app", {
    name: title,
    body: body,
    mini_app_embed: true,
    properties: { miniapp_name: name }
  });
  if (!res.ok) return { ok: false, error: res.error };

  var out = { ok: true, name: name, title: title, object: res.object, id: res.object && res.object.id };
  if (guard.injected) out.warnings = ["auto-injected missing runtime script(s): " + guard.injected.join(", ")];
  return out;
}

export function updateMiniApp(opts) {
  if (!opts || typeof opts !== "object") return { ok: false, error: "opts object required" };
  var name = opts.name;
  if (!name) return { ok: false, error: "opts.name is required" };

  var obj = _loadFull(name);
  if (!obj) return { ok: false, error: "mini app '" + name + "' not found" };

  var md = obj.markdown || "";
  var warnings = [];

  if (opts.source != null) {
    var guard = _ensureRuntimeScripts(opts.source);
    md = _replaceBlock(md, "source", guard.html);
    if (guard.injected) warnings.push("auto-injected missing runtime script(s): " + guard.injected.join(", "));
  }

  if (opts.state != null) {
    var st = _serializeState(opts.state);
    if (!st.ok) return { ok: false, name: name, error: st.error };
    md = _replaceBlock(md, "state", st.text);
  }

  if (opts.readme != null) md = _replaceReadme(md, opts.readme);

  var patch = { markdown: md, mini_app_embed: true };
  if (opts.title) patch.name = opts.title;

  var res = _c().updateObject(obj.id, patch);
  if (!res.ok) return { ok: false, name: name, error: res.error };

  var out = { ok: true, name: name, object: res.object };
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

  var obj = _loadFull(name);
  if (!obj) return { ok: false, name: name, error: "mini app '" + name + "' not found" };

  var parts = _parseBody(obj.markdown || "");
  var target = block === "source" ? parts.source : parts.state;
  if (target == null || target === "") {
    return { ok: false, name: name, block: block, error: "block '" + block + "' is empty or missing" };
  }

  var r = editString(target, oldString, newString, replaceAll);
  if (!r.ok) return { ok: false, name: name, block: block, error: r.error, lengthBefore: target.length };

  var newBlock = r.result;
  if (block === "source") {
    newBlock = _ensureRuntimeScripts(newBlock).html;
  }
  var newMd = _replaceBlock(obj.markdown || "", block, newBlock);

  var res = _c().updateObject(obj.id, { markdown: newMd, mini_app_embed: true });
  if (!res.ok) return { ok: false, name: name, error: res.error };

  return {
    ok: true,
    name: name,
    block: block,
    replacements: r.replacements,
    lengthBefore: target.length,
    lengthAfter: newBlock.length,
    object: res.object
  };
}

export function setState(name, stateObject) {
  if (!name) return { ok: false, error: "name is required" };
  if (stateObject === undefined) return { ok: false, name: name, error: "stateObject is required" };
  var st = _serializeState(stateObject);
  if (!st.ok) return { ok: false, name: name, error: st.error };

  var obj = _loadFull(name);
  if (!obj) return { ok: false, name: name, error: "mini app '" + name + "' not found" };

  var newMd = _replaceBlock(obj.markdown || "", "state", st.text);
  var res = _c().updateObject(obj.id, { markdown: newMd, mini_app_embed: true });
  if (!res.ok) return { ok: false, name: name, error: res.error };
  return { ok: true, name: name, object: res.object };
}

export function getState(name) {
  if (!name) return null;
  var obj = _loadFull(name);
  if (!obj) return null;
  var parts = _parseBody(obj.markdown || "");
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
  var obj = _loadFull(name);
  if (!obj) return { ok: false, name: name, error: "mini app '" + name + "' not found" };
  var newMd = _replaceReadme(obj.markdown || "", newReadme);
  var res = _c().updateObject(obj.id, { markdown: newMd, mini_app_embed: true });
  if (!res.ok) return { ok: false, name: name, error: res.error };
  return { ok: true, name: name, object: res.object };
}

export function upsertMiniAppState(name, source) {
  if (!name) return { ok: false, error: "name is required" };
  if (typeof source !== "string") {
    return { ok: false, name: name, error: "source must be a string (raw JSON text)" };
  }
  var obj = _loadFull(name);
  if (!obj) return { ok: false, name: name, error: "mini app '" + name + "' not found" };
  var existed = _findBlockSpan(obj.markdown || "", "## State") !== null;
  var newMd = _replaceBlock(obj.markdown || "", "state", source);
  var res = _c().updateObject(obj.id, { markdown: newMd, mini_app_embed: true });
  if (!res.ok) return { ok: false, name: name, error: res.error };
  return { ok: true, name: name, created: !existed, object: res.object };
}

export function getMiniApp(name, opts) {
  if (!name) return null;
  var obj = _loadFull(name);
  if (!obj) return null;
  var parts = _parseBody(obj.markdown || "");
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
  var obj = _loadFull(name);
  if (!obj) return null;
  var parts = _parseBody(obj.markdown || "");
  if (opts && (opts.from != null || opts.to != null)) {
    var slice = _sliceLines(parts.source, opts.from, opts.to);
    return { name: name, source: slice.text, range: slice.range };
  }
  return { name: name, source: parts.source };
}

export function listMiniApps() {
  var all = _c().getObjects("anytype_mini_app");
  var out = [];
  for (var i = 0; i < all.length; i++) {
    var n = _miniappName(all[i]);
    if (!n) continue;
    out.push({ id: all[i].id, name: n, title: all[i].name });
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
