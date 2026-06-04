// __main_source
/**
 * anyHelper — drop-in replacement for anytypeHelper, backed by the `any` HTTP API.
 * Same method shapes, different implementation.
 */

// ==================== UTILITY HELPERS (copied from anytypeHelper) ====================

// getProp reads a value off a normalized record. Records are nested per
// type (`record.<TypeName>.<xKey>`), so propKey may be a dotted path
// ("Movie.title", "any.types", "nav.parentId") which is traversed segment
// by segment. A bare key reads a top-level field (hoisted `name`/`id`, or a
// builtin namespace object). Returns null on any missing segment.
export function getProp(obj, propKey) {
  if (!obj || propKey == null) return null;
  if (propKey.indexOf(".") !== -1) {
    var parts = propKey.split(".");
    var cur = obj;
    for (var i = 0; i < parts.length; i++) {
      if (cur == null || typeof cur !== "object") return null;
      cur = cur[parts[i]];
    }
    return cur === undefined ? null : cur;
  }
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
  // Nav folder agent debug pages are filed under. Empty = leave at root.
  const debugFolderId = params.debugFolderId || "";

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
    var code = null;
    if (!res.ok) {
      error = (data && data.error && data.error.message) || (data && data.message) || ("HTTP " + res.status);
      // The server's uniform envelope is {error:{code,message,details}} (see
      // docs/06-errors.md). Surface the typed code (property.kind_mismatch,
      // property.not_found, sdk.not_implemented, space.not_found, …) so callers
      // can switch on it instead of regexing the message. Falls back to a
      // status-derived code when the body isn't the standard envelope.
      code = (data && data.error && data.error.code) || _codeForStatus(res.status);
    }
    return { ok: res.ok, status: res.status, data: data, error: error, code: code };
  };

  function _codeForStatus(status) {
    if (status === 404) return "request.not_found";
    if (status === 501) return "sdk.not_implemented";
    if (status === 503) return "server.unavailable";
    if (status >= 500) return "internal";
    if (status >= 400) return "request.bad";
    return null;
  }

  const _extractError = (apiResult) => {
    if (typeof apiResult.error === "string") return apiResult.error;
    if (apiResult.data && apiResult.data.error && apiResult.data.error.message) return apiResult.data.error.message;
    if (apiResult.data && apiResult.data.message) return apiResult.data.message;
    return "Unknown error (HTTP " + apiResult.status + ")";
  };

  // ==================== TYPE RESOLUTION ====================

  // _fetchTypes returns the scope's type list, served from the memoized
  // catalog (see TYPE / PROPERTY CATALOG below) so repeated resolves don't
  // re-hit the server.
  function _fetchTypes(scope) {
    return _cat(scope || "user").types;
  }

  function _resolveTypeId(typeId, scope) {
    var types = _fetchTypes(scope);
    for (var i = 0; i < types.length; i++) {
      if (types[i].id === typeId) return typeId;
    }
    return null;
  }

  // (Type resolution by display name was removed — types resolve by xKey/id
  // only; see _resolveTypeSeg. The name is display-only metadata.)

  function _typeNotFoundError(typeKey, scope) {
    var types = _fetchTypes(scope);
    var available = [];
    for (var i = 0; i < types.length; i++) {
      // List the xKey — the stable handle callers pass back in — with the
      // display name in parens for recognizability. The server reports
      // xKey=id for builtins, so xKey is always set; the `|| id` is defensive.
      var handle = types[i].xKey || types[i].id;
      available.push("\"" + handle + "\" (" + types[i].name + ")");
    }
    return "type \"" + typeKey + "\" doesn't exist. Available types: " + available.join(", ");
  }

  // ==================== TYPE / PROPERTY CATALOG ====================
  // The server stores and validates properties by CID ids: a property
  // value lives at record[typeId][propId]. Humans (and programs) want
  // readable "TypeName.xKey". This catalog memoizes the type list and each
  // type's property defs per scope so we can resolve readable names →ids on
  // write and reverse-map records →readable on read. GET /types/:id/properties
  // is uniform across builtin and user types: each prop has {id, name, xKey?,
  // kind} where for builtins `id` is the literal key (e.g. nav→parentId) and
  // for user types `id` is the CID and `xKey` is the stable caller key.

  var _catalog = {}; // scope -> { types, typeById, propsByType }

  function _cat(scope) {
    var key = scope || "user";
    if (_catalog[key]) return _catalog[key];
    var cat = { types: [], typeById: {}, propsByType: {} };
    var path = _pathForScope(scope);
    var res = api("GET", path + "/types");
    var types = (res.ok && res.data && res.data.types) || [];
    for (var i = 0; i < types.length; i++) {
      var t = types[i];
      if (cat.typeById[t.id]) continue; // dedup (nav is listed twice)
      cat.typeById[t.id] = t;
      cat.types.push(t);
    }
    _catalog[key] = cat;
    return cat;
  }

  function _catInvalidate(scope) { delete _catalog[scope || "user"]; }

  function _typeProps(scope, typeId) {
    var cat = _cat(scope);
    if (cat.propsByType[typeId]) return cat.propsByType[typeId];
    var path = _pathForScope(scope);
    var res = api("GET", path + "/types/" + typeId + "/properties");
    var props = (res.ok && res.data && res.data.properties) || [];
    cat.propsByType[typeId] = props;
    return props;
  }

  // _resolveTypeSeg: a type **xKey** or id → type id (or null). The display
  // name is NOT a resolution key — xKey is the stable programmatic handle (set
  // at createType, derived from name); name is display-only. Refreshes the
  // catalog once on miss so freshly-created types resolve.
  function _resolveTypeSeg(scope, seg, _retried) {
    var cat = _cat(scope);
    if (cat.typeById[seg]) return seg; // already an id
    for (var i = 0; i < cat.types.length; i++) {
      if (cat.types[i].xKey === seg) return cat.types[i].id;
    }
    if (!_retried) { _catInvalidate(scope); return _resolveTypeSeg(scope, seg, true); }
    return null;
  }

  // _resolvePropSeg: a prop id, xKey, or name under typeId → prop id (or null).
  function _resolvePropSeg(scope, typeId, seg, _retried) {
    var props = _typeProps(scope, typeId);
    for (var i = 0; i < props.length; i++) {
      var p = props[i];
      if (p.id === seg || p.xKey === seg || p.name === seg) return p.id;
    }
    if (!_retried) { _catInvalidate(scope); return _resolvePropSeg(scope, typeId, seg, true); }
    return null;
  }

  // _resolveGroupWrites: property writes are nested type groups — every
  // non-reserved top-level key of `data` is a type xKey/id whose value is a
  // { prop: value } map, mirroring the nested shape reads come back in:
  //   createObject("book", { name: "Dune", book: { author: "Frank Herbert" } })
  // Returns { groups: {typeId: {propId: val}} } keyed by the CID ids the
  // server writes by. Unknown keys ERROR — never silently dropped (a top-level
  // typo'd/misplaced property key once lost a whole batch of writes).
  var _reservedDataKeys = { name: 1, body: 1, markdown: 1, types: 1, space: 1 };

  function _resolveGroupWrites(scope, data) {
    var groups = {};
    for (var k in data) {
      if (!Object.prototype.hasOwnProperty.call(data, k)) continue;
      if (_reservedDataKeys[k]) continue;
      if (k === "properties") {
        return { ok: false, error: "data.properties was removed — nest properties under their type key: { book: { author: \"...\" } }" };
      }
      if (k.indexOf(".") !== -1) {
        var seg = k.substring(0, k.indexOf("."));
        return { ok: false, error: "dotted key \"" + k + "\" is not a valid data field — nest property writes under the type key: { " + seg + ": { " + k.substring(k.indexOf(".") + 1) + ": ... } }" };
      }
      var typeId = _resolveTypeSeg(scope, k);
      if (!typeId) {
        return { ok: false, error: "key \"" + k + "\" is neither a data field (name, body, markdown, types, space) nor a type. Property writes are nested per type: { " + k + ": { prop: value } }. " + _typeNotFoundError(k, scope) };
      }
      var group = data[k];
      if (group === null || typeof group !== "object" || Array.isArray(group)) {
        return { ok: false, error: "value for type group \"" + k + "\" must be a { prop: value } object, got " + (Array.isArray(group) ? "an array" : typeof group) };
      }
      for (var pk in group) {
        if (!Object.prototype.hasOwnProperty.call(group, pk)) continue;
        var propId = _resolvePropSeg(scope, typeId, pk);
        if (!propId) return { ok: false, error: "unknown property \"" + pk + "\" on type \"" + k + "\"" };
        if (!groups[typeId]) groups[typeId] = {};
        groups[typeId][propId] = group[pk];
      }
    }
    return { ok: true, groups: groups };
  }

  // ==================== QUERIES ====================

  // getObjects — the one query method. The first argument is polymorphic:
  //   - a STRING is the type xKey/id → "all objects of this type":
  //       getObjects("agent_memory")
  //   - an OBJECT is the full query:
  //       getObjects({ type, filter, sort, limit, offset, includeTotal, space })  // cross-object
  //       getObjects({ objectId, dataset, filter, sort, limit, ... })             // per-object dataset
  //   (A 2nd options arg after a string is still merged, for convenience.)
  //
  // Cross-object mode reads the per-space `objects` collection (optionally
  // type-scoped); filter/sort keys are dotted xKey paths resolved to
  // <typeId>.<propId>; records come back NORMALIZED (nested, readable).
  // Dataset mode reads one object's dataset (editor_blocks, program_source, …);
  // filter/sort keys are literal fields; records come back RAW.
  //
  // Returns the records ARRAY directly — iterate it as-is. THROWS on real
  // failures (unknown type — the message lists the available types; server
  // error; bad arguments) rather than returning a silent []. An empty array
  // means "no matches," never "something went wrong." When includeTotal was
  // requested the (page-bounded, v0.0.4) total is attached as `arr.total`.
  function getObjects(typeOrQuery, options) {
    var q;
    if (typeof typeOrQuery === "string") {
      q = {};
      if (options) { for (var ok in options) { if (Object.prototype.hasOwnProperty.call(options, ok)) q[ok] = options[ok]; } }
      q.type = typeOrQuery;
    } else {
      q = typeOrQuery || options || {};
    }
    var scope = q.space || "user";
    var path = _pathForScope(scope);
    var body = {};
    var isDataset = !!q.dataset;

    if (isDataset) {
      if (!q.objectId) throw new Error("getObjects: objectId required with dataset");
      body.objectId = q.objectId;
      body.dataset = q.dataset;
      if (q.filter) body.filter = q.filter; // literal dataset fields
      if (q.sort) body.sort = q.sort;
    } else {
      var filter = {};
      if (q.type) {
        var resolved = _resolveTypeSeg(scope, q.type);
        if (!resolved) throw new Error(_typeNotFoundError(q.type, scope));
        filter["any.types"] = resolved;
      }
      if (q.filter) {
        var extra = _resolveFilterPaths(scope, q.filter);
        for (var fk in extra) { if (Object.prototype.hasOwnProperty.call(extra, fk)) filter[fk] = extra[fk]; }
      }
      body.filter = filter;
      if (q.sort) body.sort = _resolveSortPaths(scope, q.sort);
    }
    if (q.limit !== undefined) body.limit = q.limit;
    if (q.offset !== undefined) body.offset = q.offset;
    if (q.includeTotal) body.includeTotal = true;

    var res = api("POST", path + (isDataset ? "/query" : "/objects/query"), body);
    if (!res.ok) throw new Error(_extractError(res));
    var raw = (res.data && res.data.records) || [];
    var records = [];
    for (var i = 0; i < raw.length; i++) {
      records.push(isDataset ? raw[i] : _normalize(scope, raw[i]));
    }
    if (res.data && res.data.total !== undefined && res.data.total !== null) records.total = res.data.total;
    return records;
  }

  // _resolveFilterPaths rewrites readable dotted filter keys ("Type.prop") to
  // the server's "<typeId>.<propId>". A key whose first segment isn't a known
  // type (e.g. "any.types", "nav.parentId", "_ver.id", or an already-resolved
  // id pair) passes through unchanged — builtin namespaces use literal keys
  // that _resolvePropSeg returns as-is.
  // _resolvePath maps a readable dotted path "Type.prop" to the server's
  // "<typeId>.<propId>". Paths whose first segment isn't a known type (builtin
  // namespaces any/nav, "_ver.id", already-resolved id pairs) pass through.
  function _resolvePath(scope, path) {
    var dot = path.indexOf(".");
    if (dot <= 0) return path;
    var typeId = _resolveTypeSeg(scope, path.substring(0, dot));
    if (!typeId) return path;
    var propId = _resolvePropSeg(scope, typeId, path.substring(dot + 1));
    return propId ? typeId + "." + propId : path;
  }

  function _resolveFilterPaths(scope, filter) {
    var out = {};
    for (var key in filter) {
      if (Object.prototype.hasOwnProperty.call(filter, key)) out[_resolvePath(scope, key)] = filter[key];
    }
    return out;
  }

  // _resolveSortPaths resolves each sort entry, preserving a leading "-"
  // (descending) marker around the path resolution.
  function _resolveSortPaths(scope, sort) {
    if (!Array.isArray(sort)) return sort;
    return sort.map(function (entry) {
      if (typeof entry !== "string") return entry;
      if (entry.charAt(0) === "-") return "-" + _resolvePath(scope, entry.substring(1));
      return _resolvePath(scope, entry);
    });
  }

  function getObject(objId, opts) {
    if (!opts) opts = {};
    var scope = opts.space || "user";
    var path = _pathForScope(scope);

    var propRes = api("GET", path + "/properties/" + objId);
    var obj = { id: objId };
    if (propRes.ok && propRes.data && propRes.data.record) {
      obj = _normalize(scope, propRes.data.record);
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

  // Tags aren't a distinct API on the any backend — they're ordinary array
  // properties. Fail loud rather than silently returning [] (which masked dead
  // reliance in the legacy port). To filter by tag, store a `tags` array
  // property and query it: getObjects(type, {filter:{"Type.tags":"x"}}) (scalar
  // = contains) or {$in:[...]}. See docs/09-query.md.
  function getObjectsByTag() {
    throw new Error("getObjectsByTag is not supported: tags are plain array properties now — use getObjects(type, {filter:{\"Type.tags\":value}}) (see docs/09-query.md)");
  }

  // No full-text index on the any backend yet (the tags/FTS decision is open).
  // Returns [] so the one intentional caller (amemory's hybrid ftsSearch) keeps
  // working with its keyword half inert; vector similarity carries recall. For
  // substring matching use getObjects with a $regex filter instead.
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
    var resolvedId = _resolveTypeSeg("user", typeKey);
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
      objects.push(_normalize("user", records[i]));
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

  // createObject(typeKey, data) — create an object of one or more types.
  //   data.types       — extra type xKeys/ids beyond typeKey (multitype)
  //   data.name        — display name (stored as any.name)
  //   data.body/markdown — editor markdown set after create
  //   data.<typeXKey>  — { prop: value } property group for that type,
  //                      mirroring the nested shape reads return:
  //                      createObject("book", { name: "Dune",
  //                        book: { author: "Frank Herbert", year: 1965 } })
  //                      (nav folders: { nav: { type: 2, parentId, pos } })
  // Any other key ERRORS — see _resolveGroupWrites. Property keys/types are
  // resolved to the CID ids the server writes by.
  function createObject(typeKey, data) {
    if (!data) data = {};
    var scope = data.space || "user";
    var path = _pathForScope(scope);
    var name = data.name || "";
    var body = data.body || data.markdown || "";

    var typeIds = [];
    if (typeKey) {
      var tid = _resolveTypeSeg(scope, typeKey);
      if (!tid) return { ok: false, error: _typeNotFoundError(typeKey, scope) };
      typeIds.push(tid);
    }
    if (Array.isArray(data.types)) {
      for (var ti = 0; ti < data.types.length; ti++) {
        var t2 = _resolveTypeSeg(scope, data.types[ti]);
        if (!t2) return { ok: false, error: _typeNotFoundError(data.types[ti], scope) };
        if (typeIds.indexOf(t2) === -1) typeIds.push(t2);
      }
    }

    var createBody = {};
    if (typeIds.length > 0) createBody.types = typeIds;

    var resolved = _resolveGroupWrites(scope, data);
    if (!resolved.ok) return { ok: false, error: resolved.error };

    var initProps = {};
    if (name) initProps.any = { name: name };
    for (var gk in resolved.groups) {
      if (!Object.prototype.hasOwnProperty.call(resolved.groups, gk)) continue;
      if (!initProps[gk]) initProps[gk] = {};
      var g = resolved.groups[gk];
      for (var pk in g) { if (Object.prototype.hasOwnProperty.call(g, pk)) initProps[gk][pk] = g[pk]; }
    }
    if (Object.keys(initProps).length > 0) createBody.initialProperties = initProps;

    var res = api("POST", path + "/objects", createBody);
    if (!res.ok) return { ok: false, error: _extractError(res), code: res.code };
    var objectId = res.data.objectId;

    if (body) {
      var mdRes = api("PUT", path + "/objects/" + objectId + "/editor/markdown", { content: body });
      if (!mdRes.ok) {
        return { ok: true, id: objectId, object: { id: objectId, name: name }, error: "Object created but markdown set failed: " + _extractError(mdRes) };
      }
    }

    return { ok: true, id: objectId, object: { id: objectId, name: name } };
  }

  // updateObject(objId, data) — update name / body / properties.
  //   data.<typeXKey> property groups use the same nested shape as
  //   createObject ({ book: { rating: 9 } }); writes are applied one
  //   base/:typeId PATCH per group. Property/name write failures (incl.
  //   server validation: property.not_found, kind_mismatch) are surfaced
  //   as { ok: false, error } — never silently swallowed.
  function updateObject(objId, data) {
    if (!data) data = {};
    var scope = data.space || "user";
    var path = _pathForScope(scope);
    var body = data.body || data.markdown;

    // Resolve before writing anything so a bad group doesn't land a partial
    // update (markdown applied, properties rejected).
    var resolvedU = _resolveGroupWrites(scope, data);
    if (!resolvedU.ok) return { ok: false, id: objId, error: resolvedU.error };

    if (body !== undefined) {
      var mdRes = api("PUT", path + "/objects/" + objId + "/editor/markdown", { content: body });
      if (!mdRes.ok) return { ok: false, id: objId, error: _extractError(mdRes), code: mdRes.code };
    }

    if (data.name !== undefined) {
      var nr = api("POST", path + "/properties/" + objId + "/base/any", { patch: { name: data.name } });
      if (!nr.ok) return { ok: false, id: objId, error: _extractError(nr), code: nr.code };
    }

    for (var gk2 in resolvedU.groups) {
      if (!Object.prototype.hasOwnProperty.call(resolvedU.groups, gk2)) continue;
      var pr = api("POST", path + "/properties/" + objId + "/base/" + gk2, { patch: resolvedU.groups[gk2] });
      if (!pr.ok) return { ok: false, id: objId, error: _extractError(pr), code: pr.code };
    }

    return { ok: true, id: objId, object: { id: objId } };
  }

  function deleteObject(objId) {
    var res = api("DELETE", spacePath + "/objects/" + objId);
    return { ok: res.ok, id: objId, error: res.ok ? null : _extractError(res), code: res.ok ? null : res.code };
  }

  // Append markdown to the tail of an object via the server's append-only
  // fast path. The server parses `text` into blocks and creates them past
  // the current last block in one ModifyBatch — no full-document read, no
  // diff — so each append is O(text), not O(document). This matters for
  // grow-by-append pages where the old read-modify-write-the-whole-doc path
  // made a run O(N²) in page size. (The agent debug log no longer uses this —
  // it now writes structured records to the `agent_debug_log` dataset.)
  function appendToObject(objId, text) {
    if (text == null || text === "") return { ok: true, id: objId, object: { id: objId } };
    var res = api("POST", spacePath + "/objects/" + objId + "/editor/markdown/append", { content: String(text) });
    if (!res.ok) return { ok: false, id: objId, error: _extractError(res) };
    return { ok: true, id: objId, object: { id: objId } };
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

  // ==================== DATASETS (writes) ====================
  // Built-in types store structured content in datasets (program → program_source
  // / program_description, mini_app → mini_app, editor → editor_blocks, …).
  // READS go through getObjects({ objectId, dataset, ... }) (records are raw —
  // datasets aren't type-namespaced). setRecord/deleteRecord are the writes.

  // setRecord upserts a dataset record, emitting one atomic $set op per field
  // at its own path so updating one field never rewrites the others. Pass a
  // flat { field: value } map; nested dotted paths ("a.b") are allowed.
  function setRecord(objId, dataset, recordId, fields, opts) {
    if (!opts) opts = {};
    var path = _pathForScope(opts.space || "user");
    var ops = [];
    for (var k in fields) {
      if (Object.prototype.hasOwnProperty.call(fields, k)) ops.push({ type: "$set", path: k, value: fields[k] });
    }
    if (ops.length === 0) return { ok: true, id: recordId };
    var res = api("POST", path + "/modify", {
      objectId: objId, dataset: dataset,
      records: [{ id: recordId, upsert: true, ops: ops }]
    });
    return { ok: res.ok, id: recordId, error: res.ok ? null : _extractError(res), code: res.ok ? null : res.code };
  }

  // deleteRecord tombstones one or more records in a dataset (read via
  // getObjects dataset mode; write via setRecord). recordIds may be a single
  // id or an array.
  function deleteRecord(objId, dataset, recordIds, opts) {
    if (!opts) opts = {};
    var path = _pathForScope(opts.space || "user");
    var ids = Array.isArray(recordIds) ? recordIds : [recordIds];
    if (ids.length === 0) return { ok: true };
    var res = api("POST", path + "/delete-records", { objectId: objId, dataset: dataset, recordIds: ids });
    return { ok: res.ok, error: res.ok ? null : _extractError(res), code: res.ok ? null : res.code };
  }

  // ==================== TAGS ====================
  // There is no select/multi_select tag API on the any backend — tags are
  // ordinary array properties. These legacy no-ops fail loud so any remaining
  // reliance surfaces instead of silently doing nothing. To tag: give the type
  // a `tags` array property and write it via createObject/updateObject; filter
  // via getObjects {filter:{"Type.tags":...}}. See docs/09-query.md.
  function _tagsUnsupported() {
    throw new Error("tag operations are not supported: use a `tags` array property (createObject/updateObject { typeXKey: { tags: [...] } }) and getObjects array filters — see docs/09-query.md");
  }
  function setTags()  { return _tagsUnsupported(); }
  function addTag()   { return _tagsUnsupported(); }
  function listTags() { return _tagsUnsupported(); }
  function createTag() { return _tagsUnsupported(); }

  // ==================== COLLECTIONS (nav folders) ====================

  // A collection is a nav folder: an object with nav.type=2. nav must be set
  // under initialProperties (the server ignores a top-level `nav` on create) —
  // the nav property group routes there like any other type group.
  function createCollection(name) {
    var res = createObject(null, { name: name, nav: { type: 2, parentId: "", pos: "" } });
    if (!res.ok) return { ok: false, error: res.error };
    return { ok: true, id: res.id, collection: { id: res.id, name: name }, object: { id: res.id, name: name } };
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
      var rec = _normalize(scope, records[i]);
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

    // Write source + (optional) tool-doc datasets. setRecord upserts the
    // single "main" record per dataset; same shape the program type expects.
    function writeProgramDatasets(objId) {
      var sr = setRecord(objId, "program_source", "main", { code: source });
      if (!sr.ok) return sr;
      if (markdown != null) {
        var dr = setRecord(objId, "program_description", "main", { text: markdown });
        if (!dr.ok) return dr;
      }
      return { ok: true };
    }

    var existing = getProgram(progName, version);
    if (existing) {
      var w = writeProgramDatasets(existing.id);
      if (!w.ok) return { ok: false, error: w.error };
      return { ok: true, object: { id: existing.id }, name: progName, version: version };
    }

    // The `program` builtin type uses literal property keys (name/version), so
    // initialProperties can pass them directly — no xKey→propId resolution.
    var programTypeId = _resolveTypeSeg("user", "program");
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

    var w2 = writeProgramDatasets(newId);
    if (!w2.ok) return { ok: true, object: { id: newId }, name: progName, version: version, error: "program created but dataset write failed: " + w2.error };

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

  // Map a caller-facing property "format" to a server property kind. `objects`
  // (a multi-value list of object refs, e.g. chat_history) and `object` map to
  // the array/object kinds; falls back to an explicit `kind` then string.
  var _formatToKind = { text: "string", number: "number", checkbox: "boolean", objects: "array", array: "array", object: "object", date: "string" };

  // createType is idempotent AND additive: if the type already exists it does
  // NOT early-return, it ensures each requested property is registered (adding
  // only the missing ones). This is deliberate — multiple programs declare the
  // same type with different properties (e.g. both init_agent and amemory
  // declare "Agent Memory"); an early-return would silently drop the second
  // program's properties and make its writes fail validation.
  // _slugifyXKey derives a stable snake_case programmatic key from a display
  // name: "Agent Memory" → "agent_memory", "Mini App" → "mini_app",
  // "ComicBook" → "comic_book". This is the type's xKey — the stable handle used
  // in dotted property paths, so it survives display-name renames.
  function _slugifyXKey(name) {
    return String(name)
      .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
      .replace(/[^A-Za-z0-9]+/g, "_")
      .replace(/_+/g, "_")
      .replace(/^_|_$/g, "")
      .toLowerCase();
  }

  function createType(opts) {
    if (!opts) return { ok: false, error: "opts required" };
    if (!opts.name) return { ok: false, error: "name is required" };

    var name = opts.name;
    var xKey = opts.xKey || _slugifyXKey(name);
    var created = false;
    // Idempotency keyed by the stable xKey (not the display name).
    var typeId = _resolveTypeSeg("user", xKey);
    if (!typeId) {
      var res = api("POST", spacePath + "/types", { name: name, xKey: xKey });
      if (!res.ok) return { ok: false, error: _extractError(res), code: res.code };
      typeId = res.data.typeId;
      created = true;
      _catInvalidate("user");
    }

    if (opts.properties && Array.isArray(opts.properties) && opts.properties.length > 0) {
      // Existing props on the type, so we add only what's missing.
      var existing = _typeProps("user", typeId);
      var have = {};
      for (var e = 0; e < existing.length; e++) {
        if (existing[e].xKey) have[existing[e].xKey] = true;
        if (existing[e].name) have[existing[e].name] = true;
      }
      var addedAny = false;
      for (var j = 0; j < opts.properties.length; j++) {
        var prop = opts.properties[j];
        if (have[prop.key]) continue; // already registered
        var ar = api("POST", spacePath + "/types/" + typeId + "/properties", {
          xKey: prop.key, name: prop.name || prop.key,
          kind: _formatToKind[prop.format] || prop.kind || "string"
        });
        if (!ar.ok) {
          _catInvalidate("user");
          return { ok: false, error: "type \"" + name + "\": property \"" + prop.key + "\" failed: " + _extractError(ar) };
        }
        addedAny = true;
      }
      if (addedAny) _catInvalidate("user");
    }

    return { ok: true, type: { id: typeId, name: name, xKey: xKey }, created: created };
  }

  // ==================== INTERNAL HELPERS ====================

  // _normalize turns a raw wire record into a readable, nested-per-type shape.
  // The server returns properties namespaced by type id and keyed by prop id
  // (record[typeId][propId]). We:
  //   - pass through scalar/builtin fields (id, _ver, author, createdAt, spaceId)
  //   - pass through BUILTIN namespaces (any, nav, program, …) verbatim — their
  //     prop keys are already literal (any.types, nav.parentId, program.name),
  //     and existing code reads them by those ids
  //   - reverse-map USER-type namespaces to readable form:
  //     record[typeId][propId] → out[TypeName][xKey]
  //   - hoist any.name → name
  // Unknown namespaces (catalog stale) are refreshed once.
  function _normalize(scope, rec) {
    if (!rec) return {};
    if (typeof rec === "string") {
      try { rec = JSON.parse(rec); } catch (e) { return { raw: rec }; }
    }
    var passthrough = { id: 1, _ver: 1, author: 1, createdAt: 1, spaceId: 1 };
    var cat = _cat(scope);
    // Refresh once if the record references a type id we don't know yet.
    for (var probe in rec) {
      if (!Object.prototype.hasOwnProperty.call(rec, probe)) continue;
      if (passthrough[probe]) continue;
      if (!cat.typeById[probe] && rec[probe] && typeof rec[probe] === "object" && !Array.isArray(rec[probe])) {
        _catInvalidate(scope); cat = _cat(scope); break;
      }
    }
    var out = {};
    for (var k in rec) {
      if (!Object.prototype.hasOwnProperty.call(rec, k)) continue;
      var tinfo = cat.typeById[k];
      var isUserType = tinfo && !tinfo.builtIn;
      if (!isUserType || !rec[k] || typeof rec[k] !== "object" || Array.isArray(rec[k])) {
        out[k] = rec[k]; // passthrough: builtin namespace, scalar, or unknown
        continue;
      }
      // Reverse-map a user-type namespace: propId → xKey/name.
      var props = _typeProps(scope, k);
      var labelById = {};
      for (var pi = 0; pi < props.length; pi++) {
        labelById[props[pi].id] = props[pi].xKey || props[pi].name || props[pi].id;
      }
      var readable = {};
      for (var pid in rec[k]) {
        if (!Object.prototype.hasOwnProperty.call(rec[k], pid)) continue;
        readable[labelById[pid] || pid] = rec[k][pid];
      }
      // Key the namespace by the type's STABLE xKey (not the mutable display
      // name), falling back to the id. So records read as obj["agent_memory"]
      // and dotted paths survive a type rename.
      out[tinfo.xKey || tinfo.id] = readable;
    }
    if (out.any && out.any.name) out.name = out.any.name;
    return out;
  }

  function __prepareTraces(traces) { return traces; }

  // ==================== WRAP TRACE ====================

  var w = (!params.noTrace && typeof __wrapTrace === "function")
    ? __wrapTrace
    : function(_name, fn) { return fn; };

  return {
    api: api,
    _extractError: _extractError,
    config: { baseUrl: baseUrl, spaceId: spaceId, spacePath: spacePath, debugFolderId: debugFolderId },
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
    setRecord: w("setRecord", setRecord),
    deleteRecord: w("deleteRecord", deleteRecord),
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
    // resolveType(xKeyOrId[, scope]) → type id (or null). xKey/id only — no
    // display-name resolution (name is display metadata).
    resolveType: function (seg, scope) { return _resolveTypeSeg(scope || "user", seg); },
    resolveTypeId: _resolveTypeId
  };
}

export function main(args) {
  return "anyHelper module loaded";
}
