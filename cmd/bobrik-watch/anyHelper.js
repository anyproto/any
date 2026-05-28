// __main_source
/**
 * anyHelper — drop-in replacement for anytypeHelper, backed by the `any` HTTP API.
 * Same method shapes, different implementation.
 */

// ==================== UTILITY HELPERS (copied from anytypeHelper) ====================

export function getProp(obj, propKey) {
  if (!obj) return null;
  if (obj.properties && obj.properties[propKey] !== undefined) return obj.properties[propKey];
  if (obj[propKey] !== undefined) return obj[propKey];
  return null;
}

export function getText(obj, propKey) {
  var val = getProp(obj, propKey);
  return typeof val === "string" ? val : null;
}

export function getNumber(obj, propKey) {
  var val = getProp(obj, propKey);
  return typeof val === "number" ? val : null;
}

export function getCheckbox(obj, propKey) {
  var val = getProp(obj, propKey);
  return typeof val === "boolean" ? val : false;
}

export function getTagKeys(obj, propKey) {
  var val = getProp(obj, propKey);
  if (Array.isArray(val)) return val;
  return [];
}

export function getSelectKey(obj, propKey) {
  var val = getProp(obj, propKey);
  return typeof val === "string" ? val : null;
}

export function getDisplayName(obj) {
  if (!obj) return "Untitled";
  if (obj.name) return obj.name;
  if (obj.snippet) {
    var newline = obj.snippet.indexOf("\n");
    return newline !== -1 ? obj.snippet.substring(0, newline) : obj.snippet;
  }
  return "Untitled";
}

export function getAllText(obj, propKey) {
  var val = getProp(obj, propKey);
  return typeof val === "string" ? val : "";
}

export function extractCode(markdown) {
  if (!markdown) return null;
  var fencePatterns = ["```javascript", "```js", "```"];
  var start = -1;
  for (var i = 0; i < fencePatterns.length; i++) {
    start = markdown.indexOf(fencePatterns[i]);
    if (start !== -1) break;
  }
  if (start === -1) return markdown;
  var codeStart = markdown.indexOf("\n", start);
  if (codeStart === -1) return null;
  codeStart += 1;
  var codeEnd = markdown.indexOf("```", codeStart);
  if (codeEnd === -1) return markdown.substring(codeStart);
  return markdown.substring(codeStart, codeEnd);
}

export function extractMainSource(markdown) {
  if (!markdown) return null;
  var pos = 0;
  var last = null;
  while (pos < markdown.length) {
    var openIdx = markdown.indexOf("```", pos);
    if (openIdx === -1) return last;
    var nl = markdown.indexOf("\n", openIdx);
    if (nl === -1) return last;
    var bodyStart = nl + 1;
    var closeIdx = markdown.indexOf("```", bodyStart);
    if (closeIdx === -1) return last;
    if (markdown.indexOf("// __main_source", bodyStart) === bodyStart) {
      last = markdown.substring(bodyStart, closeIdx);
    }
    pos = closeIdx + 3;
  }
  return last;
}

export function extractMarkdownSection(markdown, sectionName) {
  if (!markdown) return null;
  const lines = markdown.split("\n");
  let capturing = false;
  let captureLevel = 0;
  const captured = [];
  for (const line of lines) {
    const headingMatch = line.match(/^(#{1,6})\s+(.+)$/);
    if (headingMatch) {
      const level = headingMatch[1].length;
      const title = headingMatch[2].trim();
      if (capturing) {
        if (level <= captureLevel) break;
      }
      if (title.toLowerCase() === sectionName.toLowerCase()) {
        capturing = true;
        captureLevel = level;
        continue;
      }
    }
    if (capturing) {
      captured.push(line);
    }
  }
  return captured.length > 0 ? captured.join("\n").trim() : null;
}

export function editString(source, oldString, newString, replaceAll) {
  if (source == null) return { ok: false, error: "source is required" };
  if (typeof oldString !== "string" || oldString.length === 0) {
    return { ok: false, error: "oldString is required and must be non-empty" };
  }
  if (typeof newString !== "string") {
    return { ok: false, error: "newString must be a string" };
  }
  if (oldString === newString) {
    return { ok: false, error: "oldString and newString are identical" };
  }
  var count = 0;
  var pos = source.indexOf(oldString);
  while (pos !== -1) {
    count++;
    pos = source.indexOf(oldString, pos + oldString.length);
  }
  if (count === 0) {
    return { ok: false, error: "No match found for oldString" };
  }
  if (count > 1 && !replaceAll) {
    return {
      ok: false,
      error: "Found " + count + " matches for oldString; provide more surrounding context to make it unique (or set replaceAll: true)"
    };
  }
  var result;
  if (replaceAll) {
    result = source.split(oldString).join(newString);
  } else {
    var idx = source.indexOf(oldString);
    result = source.substring(0, idx) + newString + source.substring(idx + oldString.length);
  }
  return { ok: true, result: result, replacements: count };
}

export function replaceMarkdownSection(md, headingMatcher, newBody, opts) {
  if (md == null) md = "";
  opts = opts || {};
  var lines = md.split("\n");
  var matchFn;
  if (typeof headingMatcher === "string") {
    var target = headingMatcher;
    matchFn = function(line) { return line === target; };
  } else if (typeof headingMatcher === "function") {
    matchFn = headingMatcher;
  } else {
    return { ok: false, error: "headingMatcher must be a string or function" };
  }
  var startIdx = -1;
  for (var i = 0; i < lines.length; i++) {
    if (matchFn(lines[i])) { startIdx = i; break; }
  }
  if (startIdx === -1) {
    var newHeadingLine = typeof headingMatcher === "string" ? headingMatcher : opts.headingLine;
    if (!newHeadingLine) {
      return { ok: false, error: "section not found and opts.headingLine not provided for insertion" };
    }
    var hm = newHeadingLine.match(/^(#+)\s/);
    var lvl = hm ? hm[1].length : 2;
    var insertAt = lines.length;
    if (lvl === 2) {
      for (var j = 0; j < lines.length; j++) {
        if (lines[j] === "## Tool Schema") { insertAt = j; break; }
      }
    } else if (lvl === 3) {
      var schemaIdx = -1;
      for (var k = 0; k < lines.length; k++) {
        if (lines[k] === "## Tool Schema") { schemaIdx = k; break; }
      }
      if (schemaIdx !== -1) {
        insertAt = lines.length;
        for (var m2 = schemaIdx + 1; m2 < lines.length; m2++) {
          if (/^##\s/.test(lines[m2])) { insertAt = m2; break; }
        }
      }
    }
    var block = [newHeadingLine, "", newBody, ""];
    var before = lines.slice(0, insertAt);
    var after = lines.slice(insertAt);
    if (before.length > 0 && before[before.length - 1] !== "") block.unshift("");
    return { ok: true, result: before.concat(block).concat(after).join("\n"), created: true };
  }
  var headingLine = lines[startIdx];
  var hmExisting = headingLine.match(/^(#+)\s/);
  var existingLevel = hmExisting ? hmExisting[1].length : 2;
  var endIdx = lines.length;
  for (var p = startIdx + 1; p < lines.length; p++) {
    var hMatch = lines[p].match(/^(#+)\s/);
    if (hMatch && hMatch[1].length <= existingLevel) { endIdx = p; break; }
  }
  var rebuilt = lines.slice(0, startIdx + 1)
    .concat([""])
    .concat(newBody.split("\n"))
    .concat([""])
    .concat(lines.slice(endIdx));
  return { ok: true, result: rebuilt.join("\n"), created: false };
}

export const VALID_COLORS = [
  "grey", "yellow", "orange", "red", "pink",
  "purple", "blue", "ice", "teal", "lime"
];

// ==================== AUTH (no-op — localhost, no auth) ====================

export function requestChallenge() {
  return { ok: true, challenge_id: "no-auth" };
}

export function solveChallenge() {
  return { ok: true, api_key: "" };
}

export function listSpaces(params) {
  var baseUrl = params.baseUrl || params.apiBaseUrl;
  var res = fetch(baseUrl + "/v1/spaces");
  if (!res.ok) {
    return { ok: false, error: res.body || { message: "Failed to list spaces: " + res.status } };
  }
  return { ok: true, spaces: (res.body && res.body.spaces) || [] };
}

// ==================== CLIENT ====================

export function createClient(params) {
  if (!params) params = {};
  const baseUrl = params.apiBaseUrl || params.baseUrl;
  const spaceId = params.spaceId;
  const systemSpaceId = (params.systemSpaceId && params.systemSpaceId !== spaceId)
    ? params.systemSpaceId
    : null;

  const spacePath = "/v1/spaces/" + spaceId;
  const systemSpacePath = systemSpaceId ? "/v1/spaces/" + systemSpaceId : null;

  function _pathForScope(scope) {
    if (scope === "system" && systemSpacePath) return systemSpacePath;
    return spacePath;
  }

  const api = (method, path, body) => {
    const opts = {
      method: method,
      headers: { "Content-Type": "application/json" }
    };
    if (body) opts.body = JSON.stringify(body);
    const res = fetch(baseUrl + path, opts);
    var data = res.body;
    if (typeof data === "string") {
      try { data = JSON.parse(data); } catch(e) {}
    }
    var error = null;
    if (!res.ok) {
      error = (data && data.error && data.error.message) || (data && data.message) || ("HTTP " + res.status);
    }
    return { ok: res.ok, status: res.status, data: data, error: error };
  };

  const _extractError = (apiResult) => {
    if (typeof apiResult.error === "string") return apiResult.error;
    if (apiResult.data && apiResult.data.error && apiResult.data.error.message) return apiResult.data.error.message;
    if (apiResult.data && apiResult.data.message) return apiResult.data.message;
    return "Unknown error (HTTP " + apiResult.status + ")";
  };

  // ==================== TYPE RESOLUTION ====================

  function _fetchTypes(scope) {
    var path = _pathForScope(scope || "user");
    var res = api("GET", path + "/types");
    if (!res.ok) return [];
    return (res.data && res.data.types) || [];
  }

  function _resolveTypeId(typeId, scope) {
    var types = _fetchTypes(scope);
    for (var i = 0; i < types.length; i++) {
      if (types[i].id === typeId) return typeId;
    }
    return null;
  }

  function _resolveTypeByName(name, scope) {
    var types = _fetchTypes(scope);
    for (var i = 0; i < types.length; i++) {
      if (types[i].name === name) return types[i].id;
    }
    return null;
  }

  function _typeNotFoundError(typeKey, scope) {
    var types = _fetchTypes(scope);
    var available = [];
    for (var i = 0; i < types.length; i++) {
      available.push("\"" + types[i].id + "\" (" + types[i].name + ")");
    }
    return "type \"" + typeKey + "\" doesn't exist. Available types: " + available.join(", ");
  }

  // ==================== QUERIES ====================

  function getObjects(typeKey, options) {
    if (!options) options = {};
    var scope = options.space || "user";
    var path = _pathForScope(scope);
    var filter = {};
    if (typeKey) {
      var resolved = _resolveTypeByName(typeKey, scope) || _resolveTypeId(typeKey, scope);
      if (!resolved) return [];
      filter["any.types"] = resolved;
    }
    var res = api("POST", path + "/objects/query", { filter: filter });
    if (!res.ok) return [];
    var records = (res.data && res.data.records) || [];
    var objects = [];
    for (var i = 0; i < records.length; i++) {
      objects.push(normalizeRecord(records[i]));
    }
    objects.pagination = { total: objects.length };
    return objects;
  }

  function getObject(objId, opts) {
    if (!opts) opts = {};
    var scope = opts.space || "user";
    var path = _pathForScope(scope);

    var propRes = api("GET", path + "/properties/" + objId);
    var obj = { id: objId };
    if (propRes.ok && propRes.data && propRes.data.record) {
      obj = normalizeRecord(propRes.data.record);
    }

    var mdRes = api("GET", path + "/objects/" + objId + "/editor/markdown");
    if (mdRes.ok && mdRes.data) {
      obj.markdown = mdRes.data.content || "";
      obj.body = obj.markdown;
    }

    // Programs store their tool description + method docs in the
    // `program_description` dataset, not in editor blocks. Surface it on
    // `markdown`/`body` so downstream code (the boot prelude's tool-doc
    // parser, anyPrograms' section editors) sees the same shape it
    // expects for editor-backed objects.
    if (!obj.markdown && obj.program) {
      var pdRes = api("POST", path + "/query", { objectId: objId, dataset: "program_description" });
      if (pdRes.ok && pdRes.data && pdRes.data.records && pdRes.data.records.length > 0) {
        obj.markdown = pdRes.data.records[0].text || "";
        obj.body = obj.markdown;
      }
    }

    if (opts.from || opts.to) {
      var lines = (obj.markdown || "").split("\n");
      var total = lines.length;
      var from = opts.from || 1;
      var to = opts.to || total;
      obj.markdown = lines.slice(from - 1, to).join("\n");
      obj.body = obj.markdown;
      obj.range = { from: from, to: to, totalLines: total };
    }

    return obj;
  }

  // TODO: no select/multi_select property format in the any API yet
  function getObjectsByTag() { return []; }

  // TODO: no FTS indexer in the any API yet
  function search() { return []; }

  function getTypes(opts) {
    var res = api("GET", spacePath + "/types");
    if (!res.ok) return [];
    return (res.data && res.data.types) || [];
  }

  function getProperties() {
    var types = getTypes();
    var props = [];
    var seen = {};
    for (var i = 0; i < types.length; i++) {
      var propRes = api("GET", spacePath + "/types/" + types[i].id + "/properties");
      if (propRes.ok && propRes.data && propRes.data.properties) {
        var tProps = propRes.data.properties;
        for (var j = 0; j < tProps.length; j++) {
          var p = tProps[j];
          var dedupKey = p.xKey || p.id || p.name;
          if (!seen[dedupKey]) {
            seen[dedupKey] = true;
            props.push(p);
          }
        }
      }
    }
    return props;
  }

  function getProperty(propKey) {
    var all = getProperties();
    for (var i = 0; i < all.length; i++) {
      if (all[i].xKey === propKey || all[i].id === propKey || all[i].name === propKey) return all[i];
    }
    return null;
  }

  function describeType(typeKey) {
    var resolvedId = _resolveTypeByName(typeKey) || _resolveTypeId(typeKey);
    if (!resolvedId) return { error: _typeNotFoundError(typeKey) };
    var types = getTypes();
    var typeObj = null;
    for (var i = 0; i < types.length; i++) {
      if (types[i].id === resolvedId) { typeObj = types[i]; break; }
    }
    if (!typeObj) return { error: _typeNotFoundError(typeKey) };
    var propRes = api("GET", spacePath + "/types/" + typeObj.id + "/properties");
    var properties = (propRes.ok && propRes.data && propRes.data.properties) || [];
    var sample = null;
    var objects = getObjects(typeKey);
    if (objects.length > 0) sample = objects[0];
    return { type: typeObj, properties: properties, object_count: objects.length, sample: sample };
  }

  // Collections are nav folders (nav.type=2). Children have nav.parentId = folderId.
  function getCollectionObjects(collectionId) {
    var res = api("POST", spacePath + "/objects/query", {
      filter: { "nav.parentId": collectionId },
      sort: ["nav.pos"]
    });
    if (!res.ok) return [];
    var records = (res.data && res.data.records) || [];
    var objects = [];
    for (var i = 0; i < records.length; i++) {
      objects.push(normalizeRecord(records[i]));
    }
    return objects;
  }

  function getSpaceMember(identityOrId) {
    if (!identityOrId) return { ok: false, error: "identityOrId is required" };
    var res = api("GET", spacePath + "/members/" + identityOrId);
    if (!res.ok) return { ok: false, error: _extractError(res), status: res.status };
    return res.data;
  }

  function listSpaceMembers() {
    var res = api("GET", spacePath + "/members");
    if (!res.ok) return [];
    return (res.data && res.data.members) || [];
  }

  // ==================== TOOL DISCOVERY ====================

  // The boot prelude emits `var <name>;` per tool and the system prompt
  // references each tool bare (`### <name>`), so a tool name must be a
  // valid JS identifier. Anything else (e.g. `hn-top10-summary`) would
  // crash bootstrap with `Unexpected token -`. Enforced at both ends:
  // `getTools` filters offenders out, `saveProgram` rejects them on
  // write so the bad name never reaches storage.
  function _isValidProgramName(name) {
    return typeof name === "string" && /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name);
  }

  function getTools() {
    var programs = listPrograms();
    var tools = [];
    for (var i = 0; i < programs.length; i++) {
      var p = programs[i];
      if (!_isValidProgramName(p.name)) continue;
      var description = null;
      try {
        var path = _pathForScope(p.space || "user");
        var dRes = api("POST", path + "/query", { objectId: p.id, dataset: "program_description" });
        if (dRes.ok && dRes.data && dRes.data.records && dRes.data.records.length > 0) {
          description = dRes.data.records[0].text || null;
        }
      } catch (e) {}
      if (description) {
        tools.push({
          id: p.id, name: p.name, description: description,
          programName: p.name, programVersion: p.version,
          space: p.space || "user"
        });
      }
    }
    // anyHelper is always a tool
    var hasHelper = false;
    for (var j = 0; j < tools.length; j++) {
      if (tools[j].programName === "anyHelper") { hasHelper = true; break; }
    }
    if (!hasHelper) {
      tools.push({
        id: "builtin:anyHelper", name: "anyHelper",
        description: "Core API library for creating, reading, updating, and deleting objects, types, and programs.",
        programName: "anyHelper", programVersion: "v1", space: "user"
      });
    }
    return tools;
  }

  function fetchTraceSchema(traceObjectId) {
    var obj = getObject(traceObjectId);
    if (!obj || !obj.markdown) return null;
    return extractMarkdownSection(obj.markdown, "Trace Schema");
  }

  function fetchTrace(traceObjectId) {
    var obj = getObject(traceObjectId);
    if (!obj || !obj.markdown) return null;
    var section = extractMarkdownSection(obj.markdown, "Trace");
    if (!section) return null;
    try { return JSON.parse(section); } catch (e) { return null; }
  }

  // ==================== MUTATIONS ====================

  function createObject(typeKey, data) {
    if (!data) data = {};
    var name = data.name || "";
    var body = data.body || data.markdown || "";

    var createBody = {};
    var resolvedType = null;
    if (typeKey) {
      resolvedType = _resolveTypeByName(typeKey) || _resolveTypeId(typeKey);
      if (!resolvedType) return { ok: false, error: _typeNotFoundError(typeKey) };
      createBody.types = [resolvedType];
    }
    var initProps = {};
    if (name) {
      initProps.any = { name: name };
    }
    if (data.properties) {
      var propObj = {};
      if (Array.isArray(data.properties)) {
        for (var i = 0; i < data.properties.length; i++) {
          var p = data.properties[i];
          if (p.objects !== undefined) {
            propObj[p.key] = p.objects;
          } else {
            propObj[p.key] = p.text !== undefined ? p.text
              : p.number !== undefined ? p.number
              : p.checkbox !== undefined ? p.checkbox
              : p.value !== undefined ? p.value : "";
          }
        }
      } else {
        propObj = data.properties;
      }
      if (resolvedType) {
        initProps[resolvedType] = propObj;
      }
    }
    if (Object.keys(initProps).length > 0) {
      createBody.initialProperties = initProps;
    }

    var res = api("POST", spacePath + "/objects", createBody);
    if (!res.ok) {
      return { ok: false, error: _extractError(res) };
    }
    var objectId = res.data.objectId;

    if (body) {
      var mdRes = api("PUT", spacePath + "/objects/" + objectId + "/editor/markdown", { content: body });
      if (!mdRes.ok) {
        return { ok: true, id: objectId, object: { id: objectId, name: name }, error: "Object created but markdown set failed: " + _extractError(mdRes) };
      }
    }

    var obj = { id: objectId, name: name };
    return { ok: true, id: objectId, object: obj };
  }

  function updateObject(objId, data) {
    if (!data) data = {};
    var body = data.body || data.markdown;

    if (body !== undefined) {
      var mdRes = api("PUT", spacePath + "/objects/" + objId + "/editor/markdown", { content: body });
      if (!mdRes.ok) {
        return { ok: false, id: objId, error: _extractError(mdRes) };
      }
    }

    if (data.name !== undefined) {
      api("POST", spacePath + "/properties/" + objId + "/base/any", {
        patch: { name: data.name }
      });
    }

    if (data.properties) {
      // Find the object's primary type to set properties under the right namespace
      var existing = getObject(objId);
      var typeId = null;
      if (existing && existing.any && existing.any.types) {
        for (var t = 0; t < existing.any.types.length; t++) {
          var tid = existing.any.types[t];
          if (tid !== "nav" && tid !== "any" && tid !== "editor") { typeId = tid; break; }
        }
      }
      if (typeId) {
        var patch = {};
        if (Array.isArray(data.properties)) {
          for (var i = 0; i < data.properties.length; i++) {
            var p = data.properties[i];
            if (p.objects !== undefined) {
              patch[p.key] = p.objects;
            } else {
              patch[p.key] = p.text !== undefined ? p.text
                : p.number !== undefined ? p.number
                : p.checkbox !== undefined ? p.checkbox
                : p.value !== undefined ? p.value : "";
            }
          }
        } else {
          patch = data.properties;
        }
        api("POST", spacePath + "/properties/" + objId + "/base/" + typeId, { patch: patch });
      }
    }

    return { ok: true, id: objId, object: { id: objId } };
  }

  function deleteObject(objId) {
    var res = api("DELETE", spacePath + "/objects/" + objId);
    return { ok: res.ok, id: objId, error: res.ok ? null : _extractError(res) };
  }

  function appendToObject(objId, text) {
    var obj = getObject(objId);
    if (!obj) return { ok: false, id: objId, error: "Object not found" };
    var current = obj.markdown || "";
    var newMarkdown = current ? current + "\n" + text : text;
    return updateObject(objId, { markdown: newMarkdown });
  }

  function editObject(objId, opts) {
    if (!opts) return { ok: false, id: objId, error: "opts required with oldString, newString" };
    var oldStr = opts.oldString || opts.old_str;
    var newStr = opts.newString || opts.new_str;
    var replAll = opts.replaceAll || opts.replace_all || false;

    var obj = getObject(objId);
    if (!obj) return { ok: false, id: objId, error: "Object not found: " + objId };
    var oldMarkdown = obj.markdown || "";
    var r = editString(oldMarkdown, oldStr, newStr, replAll);
    if (!r.ok) return { ok: false, id: objId, error: r.error, lengthBefore: oldMarkdown.length };
    var upd = updateObject(objId, { markdown: r.result });
    if (!upd.ok) return { ok: false, id: objId, error: "updateObject failed: " + upd.error };
    return {
      ok: true, replacements: r.replacements,
      lengthBefore: oldMarkdown.length, lengthAfter: r.result.length,
      id: objId, object: upd.object
    };
  }

  // ==================== TAGS ====================
  // TODO: any API has no select/multi_select property format yet

  function setTags()  { return { ok: false, error: "tag operations not available" }; }
  function addTag()   { return { ok: false, error: "tag operations not available" }; }
  function listTags() { return []; }
  function createTag() { return { ok: false, error: "tag operations not available" }; }

  // ==================== COLLECTIONS (nav folders) ====================

  function createCollection(name) {
    var res = api("POST", spacePath + "/objects", {
      nav: { type: 2, parentId: "", pos: "" },
      initialProperties: { any: { name: name } }
    });
    if (!res.ok) return { ok: false, error: _extractError(res) };
    var id = res.data.objectId;
    return { ok: true, id: id, collection: { id: id, name: name }, object: { id: id, name: name } };
  }

  function addToCollection(collectionId, objectIds) {
    var ids = Array.isArray(objectIds) ? objectIds : [objectIds];
    for (var i = 0; i < ids.length; i++) {
      var res = api("POST", spacePath + "/properties/" + ids[i] + "/base/nav", {
        patch: { parentId: collectionId }
      });
      if (!res.ok) return { ok: false, collectionId: collectionId, objectIds: ids, error: _extractError(res) };
    }
    return { ok: true, collectionId: collectionId, objectIds: ids };
  }

  function removeFromCollection(collectionId, objectId) {
    var res = api("POST", spacePath + "/properties/" + objectId + "/base/nav", {
      patch: { parentId: "" }
    });
    return { ok: res.ok, collectionId: collectionId, objectId: objectId, error: res.ok ? null : _extractError(res) };
  }

  // ==================== PROGRAMS ====================

  function _scanPrograms(scope) {
    var path = _pathForScope(scope);
    var programTypeId = _resolveTypeId("program", scope);
    if (!programTypeId) return [];
    var res = api("POST", path + "/objects/query", { filter: { "any.types": programTypeId } });
    if (!res.ok) return [];
    var records = (res.data && res.data.records) || [];
    var programs = [];
    for (var i = 0; i < records.length; i++) {
      var rec = normalizeRecord(records[i]);
      var progName = (rec.program && rec.program.name) || rec.name || "";
      var progVersion = (rec.program && rec.program.version) || "";
      if (!progName) continue;
      programs.push({
        id: rec.id, name: progName, version: progVersion,
        title: rec.name || progName, description: null, space: scope
      });
    }
    return programs;
  }

  function listPrograms() {
    var user = _scanPrograms("user");
    if (systemSpaceId) {
      var sys = _scanPrograms("system");
      var seen = {};
      for (var i = 0; i < user.length; i++) {
        seen[user[i].name + "@" + user[i].version] = true;
      }
      for (var j = 0; j < sys.length; j++) {
        var key = sys[j].name + "@" + sys[j].version;
        if (!seen[key]) { user.push(sys[j]); seen[key] = true; }
      }
    }
    return user;
  }

  function getProgram(name, versionOrOpts, opts) {
    var version = "v1";
    if (typeof versionOrOpts === "string") {
      version = versionOrOpts;
    } else if (typeof versionOrOpts === "object") {
      opts = versionOrOpts;
      version = opts.version || "v1";
    }
    if (!opts) opts = {};

    var programs = listPrograms();
    var match = null;
    for (var i = 0; i < programs.length; i++) {
      if (programs[i].name === name && programs[i].version === version) {
        match = programs[i]; break;
      }
    }
    if (!match) return null;

    var path = _pathForScope(match.space || "user");
    var qRes = api("POST", path + "/query", { objectId: match.id, dataset: "program_source" });
    var source = "";
    if (qRes.ok && qRes.data && qRes.data.records && qRes.data.records.length > 0) {
      source = qRes.data.records[0].code || "";
    }

    var dRes = api("POST", path + "/query", { objectId: match.id, dataset: "program_description" });
    var markdown = "";
    if (dRes.ok && dRes.data && dRes.data.records && dRes.data.records.length > 0) {
      markdown = dRes.data.records[0].text || "";
    }

    return {
      id: match.id, name: name, version: version,
      title: match.title, source: source, markdown: markdown, space: match.space
    };
  }

  function runProgram(name, args, versionOrOpts) {
    var version = "v1";
    var enableTraces = false;
    if (typeof versionOrOpts === "string") {
      version = versionOrOpts;
    } else if (typeof versionOrOpts === "object") {
      version = versionOrOpts.version || "v1";
      enableTraces = versionOrOpts.enableTraces || false;
    }

    var prog = getProgram(name, version);
    if (!prog || !prog.source) {
      return { ok: false, error: "Program '" + name + "@" + version + "' not found.", traceStringLength: 0 };
    }

    var childArgs = {};
    if (args && typeof args === "object") {
      for (var k in args) {
        if (Object.prototype.hasOwnProperty.call(args, k)) childArgs[k] = args[k];
      }
    }
    childArgs.apiBaseUrl = baseUrl;
    childArgs.apiKey = "";
    childArgs.spaceId = spaceId;
    if (systemSpaceId) childArgs.systemSpaceId = systemSpaceId;

    var result = js.eval(prog.source, childArgs);
    var out = {
      ok: !result.error, result: result.result, error: result.error || null,
      program: { name: name, version: version, id: prog.id },
      traceStringLength: JSON.stringify(result.traces || {}).length
    };
    if (enableTraces) out.traces = result.traces;
    return out;
  }

  function saveProgram(opts) {
    if (!opts) opts = {};
    var progName = opts.name;
    var version = opts.version || "v1";
    var title = opts.title || progName;
    var source = opts.source;
    // Tool docs (description prelude + `## Tool Schema` section) live in
    // the `program_description` dataset. Accept either `markdown` (the new
    // name) or `appendMarkdown` (legacy from saveTool callers).
    var markdown = opts.markdown != null ? opts.markdown : opts.appendMarkdown;
    if (!progName) return { ok: false, error: "name is required" };
    if (!source) return { ok: false, error: "source is required" };
    if (!_isValidProgramName(progName)) {
      return { ok: false, error: "name must be a valid JS identifier (letters, digits, _, $; no leading digit) — got " + JSON.stringify(progName) };
    }
    // markdown — when supplied — must carry the boot prelude's contract:
    // a `## Tool Description` section. Without it the agent shows
    // "(no description in tool's md)" and the tool is half-registered.
    // saveTool always passes markdown; saveProgram callers that don't
    // want tool docs simply pass nothing.
    if (markdown != null && !/^##\s+Tool Description\s*$/m.test(String(markdown))) {
      return { ok: false, error: "markdown must contain a '## Tool Description' section" };
    }

    var existing = getProgram(progName, version);
    if (existing) {
      api("POST", spacePath + "/modify", {
        objectId: existing.id, dataset: "program_source",
        records: [{ id: "main", upsert: true, ops: [{ type: "$set", path: "", value: { code: source } }] }]
      });
      if (markdown != null) {
        api("POST", spacePath + "/modify", {
          objectId: existing.id, dataset: "program_description",
          records: [{ id: "main", upsert: true, ops: [{ type: "$set", path: "", value: { text: markdown } }] }]
        });
      }
      return { ok: true, object: { id: existing.id }, name: progName, version: version };
    }

    var programTypeId = _resolveTypeId("program");
    if (!programTypeId) return { ok: false, error: _typeNotFoundError("program") };
    var createRes = api("POST", spacePath + "/objects", {
      types: [programTypeId],
      initialProperties: {
        any: { name: title || (progName + "@" + version) },
        program: { name: progName, version: version }
      }
    });
    if (!createRes.ok) return { ok: false, error: _extractError(createRes) };
    var newId = createRes.data.objectId;

    api("POST", spacePath + "/modify", {
      objectId: newId, dataset: "program_source",
      records: [{ id: "main", upsert: true, ops: [{ type: "$set", path: "", value: { code: source } }] }]
    });
    if (markdown != null) {
      api("POST", spacePath + "/modify", {
        objectId: newId, dataset: "program_description",
        records: [{ id: "main", upsert: true, ops: [{ type: "$set", path: "", value: { text: markdown } }] }]
      });
    }

    return { ok: true, object: { id: newId }, name: progName, version: version };
  }

  function saveTool(opts) {
    if (!opts) return { ok: false, error: "opts required" };
    if (!opts.name) return { ok: false, error: "name is required" };
    if (!opts.source) return { ok: false, error: "source is required" };
    if (!opts.schema) return { ok: false, error: "schema is required" };
    return saveProgram({
      name: opts.name, version: opts.version || "v1",
      title: opts.title || opts.name, source: opts.source,
      markdown: opts.schema
    });
  }

  // ==================== TYPE MANAGEMENT ====================

  function createType(opts) {
    if (!opts) return { ok: false, error: "opts required" };
    if (!opts.name) return { ok: false, error: "name is required" };

    var name = opts.name;

    var existingId = _resolveTypeByName(name);
    if (existingId) {
      return { ok: true, type: { id: existingId, name: name }, created: false };
    }

    var body = { name: name };
    var res = api("POST", spacePath + "/types", body);
    if (!res.ok) return { ok: false, error: _extractError(res) };
    var typeId = res.data.typeId;

    if (opts.properties && Array.isArray(opts.properties)) {
      for (var j = 0; j < opts.properties.length; j++) {
        var prop = opts.properties[j];
        var formatToKind = { text: "string", number: "number", checkbox: "boolean" };
        api("POST", spacePath + "/types/" + typeId + "/properties", {
          xKey: prop.key, name: prop.name || prop.key,
          kind: formatToKind[prop.format] || prop.kind || "string"
        });
      }
    }

    return { ok: true, type: { id: typeId, name: name }, created: true };
  }

  // ==================== INTERNAL HELPERS ====================

  function normalizeRecord(rec) {
    if (!rec) return {};
    var obj = {};
    if (typeof rec === "string") {
      try { rec = JSON.parse(rec); } catch (e) { return { raw: rec }; }
    }
    for (var k in rec) {
      if (Object.prototype.hasOwnProperty.call(rec, k)) {
        obj[k] = rec[k];
      }
    }
    if (obj.any && obj.any.name) {
      obj.name = obj.any.name;
    }
    // Flatten type-namespaced properties to top level
    var builtins = { id:1, _ver:1, any:1, nav:1, author:1, spaceId:1, createdAt:1, name:1 };
    for (var tk in obj) {
      if (builtins[tk]) continue;
      if (obj[tk] && typeof obj[tk] === "object" && !Array.isArray(obj[tk])) {
        for (var pk in obj[tk]) {
          if (Object.prototype.hasOwnProperty.call(obj[tk], pk) && !obj[pk]) {
            obj[pk] = obj[tk][pk];
          }
        }
      }
    }
    return obj;
  }

  function __prepareTraces(traces) { return traces; }

  // ==================== WRAP TRACE ====================

  var w = (!params.noTrace && typeof __wrapTrace === "function")
    ? __wrapTrace
    : function(_name, fn) { return fn; };

  return {
    api: api,
    _extractError: _extractError,
    config: { baseUrl: baseUrl, spaceId: spaceId, spacePath: spacePath },
    __prepareTraces: __prepareTraces,

    getObjects: w("getObjects", getObjects),
    getObject: w("getObject", getObject),
    getObjectsByTag: w("getObjectsByTag", getObjectsByTag),
    search: w("search", search),
    getTypes: w("getTypes", getTypes),
    getProperties: w("getProperties", getProperties),
    getProperty: w("getProperty", getProperty),
    describeType: w("describeType", describeType),
    getCollectionObjects: w("getCollectionObjects", getCollectionObjects),
    getSpaceMember: w("getSpaceMember", getSpaceMember),
    listSpaceMembers: w("listSpaceMembers", listSpaceMembers),

    getTools: w("getTools", getTools),
    fetchTraceSchema: fetchTraceSchema,
    fetchTrace: fetchTrace,

    createObject: w("createObject", createObject),
    updateObject: w("updateObject", updateObject),
    deleteObject: w("deleteObject", deleteObject),
    appendToObject: w("appendToObject", appendToObject),
    editObject: w("editObject", editObject),
    setTags: w("setTags", setTags),
    addTag: w("addTag", addTag),
    listTags: w("listTags", listTags),
    createCollection: w("createCollection", createCollection),
    addToCollection: w("addToCollection", addToCollection),
    removeFromCollection: w("removeFromCollection", removeFromCollection),
    listPrograms: w("listPrograms", listPrograms),
    getProgram: w("getProgram", getProgram),
    runProgram: w("runProgram", runProgram),
    saveProgram: w("saveProgram", saveProgram),
    saveTool: w("saveTool", saveTool),
    createType: w("createType", createType),
    resolveTypeByName: _resolveTypeByName,
    resolveTypeId: _resolveTypeId
  };
}

export function main(args) {
  return "anyHelper module loaded";
}
