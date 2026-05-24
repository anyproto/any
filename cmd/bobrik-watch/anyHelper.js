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

// ==================== AUTH (no-op for any API — localhost, no auth) ====================

export function requestChallenge(params) {
  return { ok: true, challenge_id: "no-auth" };
}

export function solveChallenge(params) {
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

  // Raw HTTP helper — no auth headers needed for the any API
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

  // ==================== QUERIES ====================

  // Resolve a type key/name to its actual ID. Built-in types (program, chat,
  // editor, etc.) use their literal ID. User-created types use bafyrei... IDs
  // looked up by name.
  var _typeIdCache = {};
  function _resolveTypeId(typeKey, scope) {
    if (_typeIdCache[typeKey]) return _typeIdCache[typeKey];
    var types = getTypes();
    for (var i = 0; i < types.length; i++) {
      var t = types[i];
      // Match by id (built-in), name, or the key passed by caller
      if (t.id === typeKey) { _typeIdCache[typeKey] = t.id; return t.id; }
    }
    // Try matching by name (e.g. "any_agent_skill" → "Agent Skill")
    // Convention: snake_case key → Title Case name with "any_" prefix stripped
    var nameFromKey = typeKey.replace(/^any_/, "").replace(/_/g, " ").replace(/\b\w/g, function(c) { return c.toUpperCase(); });
    for (var j = 0; j < types.length; j++) {
      if (types[j].name === nameFromKey) {
        _typeIdCache[typeKey] = types[j].id;
        return types[j].id;
      }
    }
    return typeKey; // fallback: use as-is
  }

  // Query objects, optionally filtering by type and/or properties
  function getObjects(typeKey, options) {
    if (!options) options = {};
    var scope = options.space || "user";
    var path = _pathForScope(scope);
    var filter = {};
    if (typeKey) {
      var resolvedType = _resolveTypeId(typeKey, scope);
      filter["any.types"] = resolvedType;
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

  // Get a single object by ID — fetches properties + markdown
  function getObject(objId, opts) {
    if (!opts) opts = {};
    var scope = opts.space || "user";
    var path = _pathForScope(scope);

    // Get properties
    var propRes = api("GET", path + "/properties/" + objId);
    var obj = { id: objId };
    if (propRes.ok && propRes.data && propRes.data.record) {
      var rec = propRes.data.record;
      if (typeof rec === "object") {
        for (var k in rec) {
          if (Object.prototype.hasOwnProperty.call(rec, k) && k !== "id") {
            obj[k] = rec[k];
          }
        }
      }
    }

    // Get markdown
    var mdRes = api("GET", path + "/objects/" + objId + "/editor/markdown");
    if (mdRes.ok && mdRes.data) {
      obj.markdown = mdRes.data.content || "";
      obj.body = obj.markdown;
    }

    // Name from properties
    if (obj.any && obj.any.name) {
      obj.name = obj.any.name;
    }

    // Line-range slicing
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
  function getObjectsByTag(typeKey, propKey, tagKey) {
    return [];
  }

  // Search — best-effort via objects/query.
  // TODO: no FTS indexer in the any API yet; returns empty for now.
  function search() {
    return [];
  }

  function getTypes(opts) {
    var res = api("GET", spacePath + "/types");
    if (!res.ok) return [];
    return (res.data && res.data.types) || [];
  }

  function getProperties() {
    // The any API doesn't have a single "list all properties" endpoint.
    // We approximate by listing types and collecting their properties.
    var types = getTypes();
    var props = [];
    var seen = {};
    for (var i = 0; i < types.length; i++) {
      var t = types[i];
      var propRes = api("GET", spacePath + "/types/" + t.id + "/properties");
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
      if (all[i].key === propKey || all[i].name === propKey) return all[i];
    }
    return null;
  }

  function describeType(typeKey) {
    var types = getTypes();
    var typeObj = null;
    for (var i = 0; i < types.length; i++) {
      if (types[i].key === typeKey || types[i].id === typeKey) { typeObj = types[i]; break; }
    }
    if (!typeObj) return { error: "Type not found: " + typeKey };
    var propRes = api("GET", spacePath + "/types/" + typeObj.id + "/properties");
    var properties = (propRes.ok && propRes.data && propRes.data.properties) || [];
    var sample = null;
    var objects = getObjects(typeKey);
    if (objects.length > 0) sample = objects[0];
    return {
      type: typeObj,
      properties: properties,
      object_count: objects.length,
      sample: sample
    };
  }

  // Collections are nav folders (nav.type=2). Children are objects with nav.parentId = folderId.
  function getCollectionObjects(collectionId, viewId) {
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

  function getTools() {
    var programs = listPrograms();
    var tools = [];
    for (var i = 0; i < programs.length; i++) {
      var p = programs[i];
      // Read program_description dataset for tool description
      var description = null;
      try {
        var path = _pathForScope(p.space || "user");
        var dRes = api("POST", path + "/query", {
          objectId: p.id,
          dataset: "program_description"
        });
        if (dRes.ok && dRes.data && dRes.data.records && dRes.data.records.length > 0) {
          description = dRes.data.records[0].text || null;
        }
      } catch (e) {}
      if (description) {
        tools.push({
          id: p.id,
          name: p.name,
          description: description,
          programName: p.name,
          programVersion: p.version,
          space: p.space || "user"
        });
      }
    }
    // anyHelper is always a tool — it's the core API library
    var hasHelper = false;
    for (var j = 0; j < tools.length; j++) {
      if (tools[j].programName === "anyHelper") { hasHelper = true; break; }
    }
    if (!hasHelper) {
      tools.push({
        id: "builtin:anyHelper",
        name: "anyHelper",
        description: "Core API library for creating, reading, updating, and deleting objects, types, and programs.",
        programName: "anyHelper",
        programVersion: "v1",
        space: "user"
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
    if (typeKey) {
      createBody.types = [_resolveTypeId(typeKey)];
    }
    var initProps = {};
    if (name) {
      initProps.any = { name: name };
    }
    // Copy extra data fields as properties on the type namespace
    if (data.properties) {
      var propObj = {};
      if (Array.isArray(data.properties)) {
        for (var i = 0; i < data.properties.length; i++) {
          var p = data.properties[i];
          propObj[p.key] = p.text || p.number || p.checkbox || p.value || "";
        }
      } else {
        propObj = data.properties;
      }
      if (typeKey) {
        initProps[typeKey] = propObj;
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

    // Set markdown if provided
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

    // Update markdown if provided
    if (body !== undefined) {
      var mdRes = api("PUT", spacePath + "/objects/" + objId + "/editor/markdown", { content: body });
      if (!mdRes.ok) {
        return { ok: false, id: objId, error: _extractError(mdRes) };
      }
    }

    // Update name via space metadata if provided
    if (data.name !== undefined) {
      // Set the any.name property
      api("POST", spacePath + "/properties/" + objId + "/base/any", {
        patch: { name: data.name }
      });
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
      ok: true,
      replacements: r.replacements,
      lengthBefore: oldMarkdown.length,
      lengthAfter: r.result.length,
      id: objId,
      object: upd.object
    };
  }

  // ==================== TAGS ====================
  // TODO: any API has no select/multi_select property format yet

  function setTags(objId, propKey, tagKeys) {
    return { ok: false, error: "tag operations not available" };
  }

  function addTag(firstArg, tagName, tagKey, color) {
    return { ok: false, error: "tag operations not available" };
  }

  function listTags(propIdOrKey) {
    return [];
  }

  function createTag(propId, name, color, key) {
    return { ok: false, error: "tag operations not available" };
  }

  // ==================== COLLECTIONS (nav folders) ====================
  // A "collection" is a folder object (nav.type=2). Adding to a collection
  // moves the object's nav.parentId to point at the folder.

  function createCollection(name, emoji) {
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

  // Find program property keys in this space.
  // Returns { nameKey, versionKey } or nulls.
  function _findProgramPropKeys() {
    var props = getProperties();
    var nameKey = null, versionKey = null;
    for (var i = 0; i < props.length; i++) {
      var p = props[i];
      // Support both any_program.name style and __anytype_program_name style
      if (p.key === "name" || p.key === "__anytype_program_name" || p.name === "name") {
        // Check if this is from the any_program type
        nameKey = p.key;
      }
      if (p.key === "version" || p.key === "__anytype_program_version" || p.name === "version") {
        versionKey = p.key;
      }
    }
    return { nameKey: nameKey, versionKey: versionKey };
  }

  function _scanPrograms(scope) {
    var path = _pathForScope(scope);
    // The registered handler type is "program"
    var res = api("POST", path + "/objects/query", { filter: { "any.types": "program" } });
    if (!res.ok) return [];
    var records = (res.data && res.data.records) || [];
    var programs = [];
    for (var i = 0; i < records.length; i++) {
      var rec = normalizeRecord(records[i]);
      // Program properties live under the type ID namespace.
      // The registered type is "program", so properties are at rec.program.name etc.
      var progName = (rec.program && rec.program.name) || "";
      var progVersion = (rec.program && rec.program.version) || "";
      if (!progName) continue;

      // Description comes from program_description dataset (future);
      // for now skip the per-program fetch to avoid N+1
      programs.push({
        id: rec.id,
        name: progName,
        version: progVersion,
        title: rec.name || progName,
        description: null,
        space: scope
      });
    }
    return programs;
  }

  function listPrograms() {
    var user = _scanPrograms("user");
    if (systemSpaceId) {
      var sys = _scanPrograms("system");
      // Merge: user wins on name@version conflict
      var seen = {};
      for (var i = 0; i < user.length; i++) {
        seen[user[i].name + "@" + user[i].version] = true;
      }
      for (var j = 0; j < sys.length; j++) {
        var key = sys[j].name + "@" + sys[j].version;
        if (!seen[key]) {
          user.push(sys[j]);
          seen[key] = true;
        }
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
        match = programs[i];
        break;
      }
    }
    if (!match) return null;

    // Read source from program_source dataset
    var path = _pathForScope(match.space || "user");
    var qRes = api("POST", path + "/query", {
      objectId: match.id,
      dataset: "program_source"
    });
    var source = "";
    if (qRes.ok && qRes.data && qRes.data.records && qRes.data.records.length > 0) {
      source = qRes.data.records[0].code || "";
    }

    return {
      id: match.id,
      name: name,
      version: version,
      title: match.title,
      source: source,
      space: match.space
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
    var nameWithVersion = name + "@" + version;
    if (!prog || !prog.source) {
      return { ok: false, error: "Program '" + nameWithVersion + "' not found. Use client.listPrograms() to see available programs.", traceStringLength: 0 };
    }

    // Build child args with client credentials
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
      ok: !result.error,
      result: result.result,
      error: result.error || null,
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
    if (!progName) return { ok: false, error: "name is required" };
    if (!source) return { ok: false, error: "source is required" };

    // Check for existing program
    var existing = getProgram(progName, version);
    if (existing) {
      // Update source via modify
      api("POST", spacePath + "/modify", {
        objectId: existing.id,
        dataset: "program_source",
        records: [{ id: "main", upsert: true, ops: [{ type: "$set", path: "", value: { code: source } }] }]
      });
      return { ok: true, object: { id: existing.id }, name: progName, version: version };
    }

    // Create new program object
    // Find the "program" type ID
    var types = getTypes();
    var programTypeId = null;
    for (var ti = 0; ti < types.length; ti++) {
      if (types[ti].id === "program") { programTypeId = "program"; break; }
    }
    if (!programTypeId) return { ok: false, error: "program type not registered" };

    var createRes = api("POST", spacePath + "/objects", {
      types: [programTypeId],
      initialProperties: {
        any: { name: title || (progName + "@" + version) },
        program: { name: progName, version: version }
      }
    });
    if (!createRes.ok) return { ok: false, error: _extractError(createRes) };
    var newId = createRes.data.objectId;

    // Write source
    api("POST", spacePath + "/modify", {
      objectId: newId,
      dataset: "program_source",
      records: [{ id: "main", upsert: true, ops: [{ type: "$set", path: "", value: { code: source } }] }]
    });

    return { ok: true, object: { id: newId }, name: progName, version: version };
  }

  function saveTool(opts) {
    if (!opts) return { ok: false, error: "opts required" };
    if (!opts.name) return { ok: false, error: "name is required" };
    if (!opts.source) return { ok: false, error: "source is required" };
    if (!opts.schema) return { ok: false, error: "schema is required" };

    return saveProgram({
      name: opts.name,
      version: opts.version || "v1",
      title: opts.title || opts.name,
      source: opts.source,
      appendMarkdown: opts.schema
    });
  }

  // ==================== TYPE MANAGEMENT ====================

  function createType(opts) {
    if (!opts) return { ok: false, error: "opts required" };
    if (!opts.key) return { ok: false, error: "key is required" };

    var name = opts.name || opts.key;
    var key = opts.key;

    // Check if type already exists
    var types = getTypes();
    var existing = null;
    for (var i = 0; i < types.length; i++) {
      if (types[i].name === name || types[i].id === key) { existing = types[i]; break; }
    }

    var typeId;
    if (existing) {
      typeId = existing.id;
    } else {
      var body = { name: name };
      var res = api("POST", spacePath + "/types", body);
      if (!res.ok) return { ok: false, error: _extractError(res) };
      typeId = res.data.typeId;
    }

    if (opts.properties && Array.isArray(opts.properties)) {
      for (var j = 0; j < opts.properties.length; j++) {
        var prop = opts.properties[j];
        var formatToKind = { text: "string", number: "number", checkbox: "boolean" };
        var propBody = {
          xKey: prop.key,
          name: prop.name || prop.key,
          kind: formatToKind[prop.format] || prop.kind || "string"
        };
        api("POST", spacePath + "/types/" + typeId + "/properties", propBody);
      }
    }

    return {
      ok: true,
      type: { id: typeId, key: key, name: name },
      created: !existing
    };
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
    // Extract name from any.name if present
    if (obj.any && obj.any.name) {
      obj.name = obj.any.name;
    }
    // Flatten type-namespaced properties to top level.
    // Type IDs are bafyrei... hashes or registered IDs like "program".
    // Any key that holds an object and isn't a known built-in namespace
    // gets its children promoted.
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

  function __prepareTraces(traces) {
    return traces;
  }

  // ==================== WRAP TRACE ====================

  var w = (!params.noTrace && typeof __wrapTrace === "function")
    ? __wrapTrace
    : function(_name, fn) { return fn; };

  return {
    api: api,
    _extractError: _extractError,
    config: { baseUrl: baseUrl, spaceId: spaceId, spacePath: spacePath },
    __prepareTraces: __prepareTraces,

    // Queries
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

    // Tool discovery
    getTools: w("getTools", getTools),

    // Trace inspection
    fetchTraceSchema: fetchTraceSchema,
    fetchTrace: fetchTrace,

    // Mutations
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
    createType: w("createType", createType)
  };
}

export function main(args) {
  return "anyHelper module loaded";
}
