import { createClient, editString, extractMarkdownSection } from "anyHelper@v1";

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

// Program names become kernel globals (`var <name> = ...` in the boot
// prelude), so they must be valid JS identifiers. Same check getTools
// applies on read.
function _isValidProgramName(name) {
  return typeof name === "string" && /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name);
}

// Callable name: heading text up to the first "(". Prose headings without a
// signature keep their full text.
function _methodBareName(name) {
  var idx = name.indexOf("(");
  if (idx > 0) return name.substring(0, idx).trim();
  return name.trim();
}

// Render one stored method record back to its markdown section. The kind tag
// is re-attached to the heading — that exact shape (`### name(sig) [kind]`)
// is what describeMethod returns and what gets injected into agent prompts.
function _renderMethodDoc(m) {
  var heading = "### " + m.name + (m.kind ? " [" + m.kind + "]" : "");
  return m.text ? heading + "\n\n" + m.text : heading;
}

// Split a full tool markdown into { description, methods } — the STORAGE
// shape: description = "## Tool Description" section body; methods = one
// entry per "### name(sig) [kind]" subsection of "## Tool Schema" (or
// "# Tools"/"## Tools"), kind defaulting to getter. Mirror of the Go
// splitter in cmd/bobrik-watch/toolmd.go — keep the two in sync.
function _splitToolMarkdown(md) {
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
  var seen = {};
  var currentName = null;
  var currentKind = "getter";
  var currentLines = [];

  function flush() {
    if (currentName === null) return;
    var bare = _methodBareName(currentName);
    if (seen[bare]) bare = bare + "-" + methods.length; // record ids must be unique
    seen[bare] = true;
    methods.push({
      bareName: bare, name: currentName, kind: currentKind,
      // Body stored without surrounding blank lines; _renderMethodDoc
      // reconstructs the section as heading + "\n\n" + text.
      text: currentLines.join("\n").replace(/^\n+/, "").replace(/\n+$/, ""),
      pos: methods.length
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
      currentLines = [];
    } else if (currentName !== null) {
      currentLines.push(lines[i]);
    }
  }
  flush();
  return { description: description, methods: methods };
}

// Write the split tool docs onto a program object: description body to
// program_description/"main", one program_methods record per method, then
// delete stale method records the new doc no longer carries.
function _writeToolDocs(c, objId, parsed) {
  var dr = c.setRecord(objId, "program_description", "main", { text: parsed.description });
  if (!dr.ok) return dr;
  var existing = [];
  try { existing = c.getObjects({ objectId: objId, dataset: "program_methods" }); } catch (e) {}
  var keep = {};
  for (var i = 0; i < parsed.methods.length; i++) {
    var m = parsed.methods[i];
    keep[m.bareName] = true;
    var mr = c.setRecord(objId, "program_methods", m.bareName, {
      name: m.name, kind: m.kind, text: m.text, pos: m.pos
    });
    if (!mr.ok) return mr;
  }
  var stale = [];
  for (var j = 0; j < existing.length; j++) {
    if (existing[j] && existing[j].id && !keep[existing[j].id]) stale.push(existing[j].id);
  }
  if (stale.length > 0) {
    var delr = c.deleteRecord(objId, "program_methods", stale);
    if (!delr.ok) return delr;
  }
  return { ok: true };
}

// saveProgram — the one program write path (moved here from anyHelper;
// anyHelper keeps only the generic primitives this composes). Two modes:
//   - source-only (no markdown): writes program_source, leaves docs and
//     any_tool untouched. Used by editProgram and source-only updates.
//   - tool save (markdown given): splits the md, writes description +
//     method records, reconciles stale ones, and sets program.any_tool=true.
//     The markdown MUST carry a non-empty "## Tool Description" AND a
//     "## Tool Schema" with at least one "### method()" subsection —
//     toolhood is all-or-nothing, no half-registered tools.
export function saveProgram(opts) {
  if (!opts) opts = {};
  var c = _getClient();
  var progName = opts.name;
  var version = opts.version || "v1";
  var title = opts.title || progName;
  var source = opts.source;
  // Accept `markdown` (canonical) or `schema` (legacy saveTool spelling).
  var markdown = opts.markdown != null ? opts.markdown : opts.schema;
  if (!progName) return { ok: false, error: "name is required" };
  if (!source) return { ok: false, error: "source is required" };
  if (!_isValidProgramName(progName)) {
    return { ok: false, error: "name must be a valid JS identifier (letters, digits, _, $; no leading digit) — got " + JSON.stringify(progName) };
  }

  var parsed = null;
  if (markdown != null) {
    parsed = _splitToolMarkdown(String(markdown));
    if (!parsed.description) {
      return { ok: false, error: "markdown must contain a non-empty '## Tool Description' section" };
    }
    if (parsed.methods.length === 0) {
      return { ok: false, error: "markdown must contain a '## Tool Schema' section with at least one '### method()' subsection" };
    }
  }

  function writeProgramDatasets(objId) {
    var sr = c.setRecord(objId, "program_source", "main", { code: source });
    if (!sr.ok) return sr;
    if (parsed) {
      var wr = _writeToolDocs(c, objId, parsed);
      if (!wr.ok) return wr;
      var ur = c.updateObject(objId, { program: { any_tool: true } });
      if (!ur.ok) return ur;
    }
    return { ok: true };
  }

  var existing = c.getProgram(progName, version);
  if (existing) {
    var w = writeProgramDatasets(existing.id);
    if (!w.ok) return { ok: false, error: w.error };
    return { ok: true, object: { id: existing.id }, name: progName, version: version };
  }

  var createRes = c.createObject("program", {
    name: title || (progName + "@" + version),
    program: { name: progName, version: version, any_tool: parsed != null }
  });
  if (!createRes.ok) return { ok: false, error: createRes.error };
  var newId = createRes.id;

  var w2 = writeProgramDatasets(newId);
  if (!w2.ok) return { ok: true, object: { id: newId }, name: progName, version: version, error: "program created but dataset write failed: " + w2.error };

  return { ok: true, object: { id: newId }, name: progName, version: version };
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

  var saveResult = saveProgram({
    name: name,
    source: source,
    markdown: markdown,
    version: version,
    title: title
  });
  if (!saveResult.ok) return { ok: false, error: "saveProgram failed: " + saveResult.error };

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
    saveResult = saveProgram({
      name: name, source: source, markdown: opts.markdown, version: version
    });
  } else {
    // Source-only update — docs and any_tool stay as the last tool save left them.
    saveResult = saveProgram({
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

  // Source-only save: deliberately NO markdown. Round-tripping the (now
  // description-only) markdown through a tool save would re-split it and
  // wipe every program_methods record — docs are not editProgram's surface.
  var saveResult = saveProgram({
    name: programName,
    source: newSource,
    version: version,
    title: prog.title || programName
  });
  if (!saveResult.ok) {
    return { ok: false, name: programName, version: version, error: "saveProgram failed: " + saveResult.error };
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
// Doc edits — write the split datasets directly: description body to
// program_description, one record per method to program_methods. any_tool is
// recomputed after every doc write (true iff non-empty description AND ≥1
// method) so toolhood tracks the docs.
// ============================================================================

function _recomputeAnyTool(prog, description, methodCount) {
  var anyTool = !!(description && methodCount > 0);
  return _getClient().updateObject(prog.id, { program: { any_tool: anyTool } });
}

export function upsertDescription(programName, newDescription, opts) {
  if (!programName) return { ok: false, error: "programName is required" };
  if (typeof newDescription !== "string") {
    return { ok: false, name: programName, error: "newDescription must be a string" };
  }
  opts = opts || {};
  var version = opts.version || "v1";

  var prog = _getClient().getProgram(programName, version);
  if (!prog) return { ok: false, name: programName, version: version, error: "program '" + programName + "@" + version + "' not found" };

  var created = !prog.description;
  var dr = _getClient().setRecord(prog.id, "program_description", "main", { text: newDescription });
  if (!dr.ok) return { ok: false, name: programName, version: version, error: dr.error };

  var ur = _recomputeAnyTool(prog, newDescription, (prog.methods || []).length);
  if (!ur.ok) return { ok: false, name: programName, version: version, error: ur.error };

  return { ok: true, name: programName, version: version, created: created, object: { id: prog.id } };
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

  var methods = prog.methods || [];
  var existing = null;
  var maxPos = -1;
  for (var i = 0; i < methods.length; i++) {
    if (methods[i].bareName === _methodBareName(methodName)) existing = methods[i];
    if (typeof methods[i].pos === "number" && methods[i].pos > maxPos) maxPos = methods[i].pos;
  }

  // Existing method: only the body changes — name/kind/pos stay (setRecord
  // sets per-field). New method: appended at the tail with a bare signature;
  // pass a full heading as methodName (e.g. "run(args) [mutator]") to set
  // the signature and kind explicitly.
  var fields, recId;
  if (existing) {
    recId = existing.bareName;
    fields = { text: newMethodDescription };
  } else {
    recId = _methodBareName(methodName);
    var headingText = methodName;
    var kind = "getter";
    var kindMatch = headingText.match(/\s*\[(getter|mutator|setup|program)\]\s*$/);
    if (kindMatch) {
      kind = kindMatch[1];
      headingText = headingText.substring(0, kindMatch.index).trim();
    }
    if (headingText.indexOf("(") === -1) headingText += "()";
    fields = { name: headingText, kind: kind, text: newMethodDescription, pos: maxPos + 1 };
  }

  var mr = _getClient().setRecord(prog.id, "program_methods", recId, fields);
  if (!mr.ok) return { ok: false, name: programName, version: version, methodName: methodName, error: mr.error };

  var methodCount = methods.length + (existing ? 0 : 1);
  var ur = _recomputeAnyTool(prog, prog.description, methodCount);
  if (!ur.ok) return { ok: false, name: programName, version: version, methodName: methodName, error: ur.error };

  return {
    ok: true,
    name: programName,
    version: version,
    methodName: methodName,
    created: !existing,
    object: { id: prog.id }
  };
}

// ============================================================================
// Doc reads — straight off the split datasets (via getProgram).
// ============================================================================
export function getProgramDescription(programName, opts) {
  if (!programName) return null;
  opts = opts || {};
  var version = opts.version || "v1";
  var prog = _getClient().getProgram(programName, version);
  if (!prog) return null;
  return prog.description || null;
}

export function getMethodDescription(programName, methodName, opts) {
  if (!programName || !methodName) return null;
  opts = opts || {};
  var version = opts.version || "v1";
  var prog = _getClient().getProgram(programName, version);
  if (!prog) return null;

  var methods = prog.methods || [];
  var bare = _methodBareName(methodName);
  for (var i = 0; i < methods.length; i++) {
    if (methods[i].bareName === bare) return _renderMethodDoc(methods[i]);
  }
  return null;
}

export function main() {
  return "anyPrograms loaded — methods: createProgram, updateProgram, saveProgram, runProgram, listPrograms, getProgram, editProgram, upsertDescription, upsertMethodDescription, getProgramDescription, getMethodDescription";
}
