import { createClient, editString, replaceMarkdownSection, extractMarkdownSection } from "anyHelper@v1";

var _client = null;
function _getClient() {
  if (_client) return _client;
  _client = createClient({
    apiBaseUrl: env.ANYTYPE_API_URL,
    apiKey: env.ANYTYPE_API_KEY,
    spaceId: env.ANYTYPE_SPACE_ID
  });
  return _client;
}

function _hasMainExport(source) {
  return /export\s+function\s+main\s*\(/.test(source);
}

// Probe the live runtime: import the just-saved tool and report its methods.
// Runs in a fresh js.eval so import failures show as a normal error string,
// not as a thrown exception that would unwind the caller. The probe uses a
// top-level `import * as ns from "name@v1"` — works in any js.eval mode and
// avoids the persistent-kernel-only `__import` helper.
function _verifyImportable(name, version) {
  var spec = name + "@" + version;
  var probeSource =
    'import * as __ns from ' + JSON.stringify(spec) + ';\n' +
    'export function main() {\n' +
    '  var methods = [];\n' +
    '  for (var k in __ns) {\n' +
    '    if (typeof __ns[k] === "function") methods.push(k);\n' +
    '  }\n' +
    '  methods.sort();\n' +
    '  return JSON.stringify({ ok: true, methods: methods });\n' +
    '}';
  var res = js.eval(probeSource, {});
  // An import failure (bad syntax, missing module, etc.) surfaces as res.error
  // since the module is loaded before main() runs.
  if (res.error) return { ok: false, methods: [], error: String(res.error) };
  try {
    return JSON.parse(res.result);
  } catch (e) {
    return { ok: false, methods: [], error: "probe returned non-JSON: " + String(res.result) };
  }
}

export function createProgram(opts) {
  if (!opts || typeof opts !== "object") return { ok: false, error: "opts object required" };
  var name = opts.name;
  var source = opts.source;
  var markdown = opts.markdown;
  var version = opts.version || "v1";
  var title = opts.title || name;

  if (!name) return { ok: false, error: "opts.name is required" };
  if (!source) return { ok: false, error: "opts.source is required" };
  if (!markdown) return { ok: false, error: "opts.markdown is required (must contain '## Tool Description' and '## Tool Schema')" };
  if (!_hasMainExport(source)) {
    return { ok: false, error: "opts.source must contain `export function main(args)`" };
  }

  var saveResult = _getClient().saveTool({
    name: name,
    source: source,
    schema: markdown,
    version: version,
    title: title
  });
  if (!saveResult.ok) return { ok: false, error: "saveTool failed: " + saveResult.error };

  var probe = _verifyImportable(name, version);
  if (!probe.ok) {
    return {
      ok: false,
      saved: true,
      object: saveResult.object,
      name: name,
      version: version,
      error: "Tool saved but failed to import: " + probe.error,
      hint: "Fix the source so `import ... from \"" + name + "@" + version + "\"` succeeds, then call updateProgram({name, source})."
    };
  }

  return {
    ok: true,
    object: saveResult.object,
    name: name,
    version: version,
    methods: probe.methods
  };
}

export function updateProgram(opts) {
  if (!opts || !opts.name) return { ok: false, error: "opts.name is required" };
  var name = opts.name;
  var version = opts.version || "v1";

  var existing = _getClient().getProgram(name, version);
  if (!existing) return { ok: false, error: "Program '" + name + "@" + version + "' not found. Use createProgram to create new." };

  var source = opts.source != null ? opts.source : existing.source;
  if (!_hasMainExport(source)) {
    return { ok: false, error: "Source must contain `export function main(args)`" };
  }

  var saveResult;
  if (opts.markdown) {
    saveResult = _getClient().saveTool({
      name: name, source: source, schema: opts.markdown, version: version
    });
  } else {
    saveResult = _getClient().saveProgram({
      name: name, source: source, version: version
    });
  }
  if (!saveResult.ok) return { ok: false, error: saveResult.error };

  var probe = _verifyImportable(name, version);
  if (!probe.ok) {
    return {
      ok: false, saved: true, object: saveResult.object, name: name, version: version,
      error: "Updated but failed to import: " + probe.error
    };
  }

  return {
    ok: true, object: saveResult.object, name: name, version: version,
    methods: probe.methods
  };
}

export function runProgram(name, args, versionOrOpts) {
  return _getClient().runProgram(name, args, versionOrOpts);
}

export function listPrograms() {
  return _getClient().listPrograms();
}

export function getProgram(name, versionOrOpts, opts) {
  return _getClient().getProgram(name, versionOrOpts, opts);
}

// Escape a string for safe literal inclusion in a RegExp.
function _reEscape(s) {
  return String(s).replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// Locate a method section inside the full program markdown. Returns either the
// zero-based start line index (int) or -1 if no heading of the form
// `### methodName(...)` is present.
function _findMethodHeadingLine(lines, methodName) {
  var re = new RegExp("^###\\s+" + _reEscape(methodName) + "\\s*\\(");
  for (var i = 0; i < lines.length; i++) {
    if (re.test(lines[i])) return i;
  }
  return -1;
}

// Stub used when editProgram drops `export function main(args)` — keeps the
// program savable (linter tolerates it) and tells the agent that main needs
// re-implementing.
var _MAIN_STUB =
  '\nexport function main(args) {\n' +
  '  return { ok: false, error: "main not implemented" };\n' +
  '}\n';

// ============================================================================
// Source edit: str_replace on a program's source code. Mirror of
// anyHelper.editObject. The program's markdown fence and the
// `// __main_source` marker are transparent — the caller matches against
// source text only.
// ============================================================================
export function editProgram(programName, opts) {
  if (!programName) return { ok: false, error: "programName is required" };
  if (!opts || typeof opts !== "object") {
    return { ok: false, name: programName, error: "opts object required with oldString, newString, replaceAll?, version?" };
  }
  var version = opts.version || "v1";
  // Silently accept snake_case aliases — agents reach for these from muscle
  // memory (other toolkits use old_str / new_str / replace_all).
  var oldString = opts.oldString != null ? opts.oldString : opts.old_str;
  var newString = opts.newString != null ? opts.newString : opts.new_str;
  var replaceAll = opts.replaceAll != null ? opts.replaceAll : opts.replace_all;

  var prog = _getClient().getProgram(programName, version);
  if (!prog) return { ok: false, name: programName, version: version, error: "program '" + programName + "@" + version + "' not found" };

  var r = editString(prog.source, oldString, newString, replaceAll);
  if (!r.ok) return { ok: false, name: programName, version: version, error: r.error, lengthBefore: (prog.source || "").length };

  var newSource = r.result;
  var message = null;
  if (!_hasMainExport(newSource)) {
    newSource = newSource.replace(/\s+$/, "") + _MAIN_STUB;
    message = "main export was missing after edit; injected stub";
  }

  var saveResult = _getClient().saveTool({
    name: programName,
    source: newSource,
    schema: prog.markdown,
    version: version,
    title: prog.title || programName
  });
  if (!saveResult.ok) {
    return { ok: false, name: programName, version: version, error: "saveTool failed: " + saveResult.error };
  }

  var out = {
    ok: true,
    name: programName,
    version: version,
    replacements: r.replacements,
    lengthBefore: (prog.source || "").length,
    lengthAfter: newSource.length,
    object: saveResult.object
  };
  if (message) out.message = message;
  return out;
}

// ============================================================================
// Doc edits — structured, section-aware. Go through saveTool so the linter
// re-runs on every save (empty Tool Description, method with no `- ` bullets
// → fail fast).
// ============================================================================
export function upsertDescription(programName, newDescription, opts) {
  if (!programName) return { ok: false, error: "programName is required" };
  if (typeof newDescription !== "string") {
    return { ok: false, name: programName, error: "newDescription must be a string" };
  }
  opts = opts || {};
  var version = opts.version || "v1";

  var prog = _getClient().getProgram(programName, version);
  if (!prog) return { ok: false, name: programName, version: version, error: "program '" + programName + "@" + version + "' not found" };

  var r = replaceMarkdownSection(prog.markdown || "", "## Tool Description", newDescription);
  if (!r.ok) return { ok: false, name: programName, version: version, error: r.error };

  var saveResult = _getClient().saveTool({
    name: programName,
    source: prog.source,
    schema: r.result,
    version: version,
    title: prog.title || programName
  });
  if (!saveResult.ok) {
    return { ok: false, name: programName, version: version, error: "saveTool failed: " + saveResult.error };
  }

  return { ok: true, name: programName, version: version, created: r.created, object: saveResult.object };
}

export function upsertMethodDescription(programName, methodName, newMethodDescription, opts) {
  if (!programName) return { ok: false, error: "programName is required" };
  if (!methodName) return { ok: false, name: programName, error: "methodName is required" };
  if (typeof newMethodDescription !== "string") {
    return { ok: false, name: programName, methodName: methodName, error: "newMethodDescription must be a string" };
  }
  opts = opts || {};
  var version = opts.version || "v1";

  var prog = _getClient().getProgram(programName, version);
  if (!prog) return { ok: false, name: programName, version: version, error: "program '" + programName + "@" + version + "' not found" };

  var md = prog.markdown || "";
  var re = new RegExp("^###\\s+" + _reEscape(methodName) + "\\s*\\(");
  var matcher = function(line) { return re.test(line); };

  var r = replaceMarkdownSection(md, matcher, newMethodDescription, {
    headingLine: "### " + methodName + "()"
  });
  if (!r.ok) return { ok: false, name: programName, version: version, methodName: methodName, error: r.error };

  var saveResult = _getClient().saveTool({
    name: programName,
    source: prog.source,
    schema: r.result,
    version: version,
    title: prog.title || programName
  });
  if (!saveResult.ok) {
    return { ok: false, name: programName, version: version, methodName: methodName, error: "saveTool failed: " + saveResult.error };
  }

  return {
    ok: true,
    name: programName,
    version: version,
    methodName: methodName,
    created: r.created,
    object: saveResult.object
  };
}

// ============================================================================
// Doc reads
// ============================================================================
export function getProgramDescription(programName, opts) {
  if (!programName) return null;
  opts = opts || {};
  var version = opts.version || "v1";
  var prog = _getClient().getProgram(programName, version);
  if (!prog) return null;
  return extractMarkdownSection(prog.markdown || "", "Tool Description");
}

export function getMethodDescription(programName, methodName, opts) {
  if (!programName || !methodName) return null;
  opts = opts || {};
  var version = opts.version || "v1";
  var prog = _getClient().getProgram(programName, version);
  if (!prog) return null;

  var lines = (prog.markdown || "").split("\n");
  var startIdx = _findMethodHeadingLine(lines, methodName);
  if (startIdx === -1) return null;

  // End at the next heading of same-or-higher level (### or ##).
  var endIdx = lines.length;
  for (var i = startIdx + 1; i < lines.length; i++) {
    if (/^###\s/.test(lines[i]) || /^##\s/.test(lines[i])) { endIdx = i; break; }
  }
  // Trim trailing blank lines from the returned section.
  while (endIdx > startIdx + 1 && lines[endIdx - 1] === "") endIdx--;
  return lines.slice(startIdx, endIdx).join("\n");
}

export function main() {
  return "anyPrograms loaded — methods: createProgram, updateProgram, runProgram, listPrograms, getProgram, editProgram, upsertDescription, upsertMethodDescription, getProgramDescription, getMethodDescription";
}
