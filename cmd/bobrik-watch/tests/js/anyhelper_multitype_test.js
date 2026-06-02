// Multitype property/type/record contract for the rewritten anyHelper.
//
// Pins the model resolved by probing the live v0.0.4 server:
//   - properties are stored/written by CID propId, namespaced per typeId
//   - GET /types/:id/properties maps propId <-> xKey <-> name (uniform for
//     builtin and user types)
//   - the helper resolves readable "Type.prop" <-> ids on write, and reverse-
//     maps records to readable nested "Type.prop" on read.
//
// Run via jsrunner_test.go (TestJSAnyHelper).

import { createClient, getProp } from "anyHelper@v1";

function mkHarness() {
  var summary = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (name, cond, detail) {
      if (cond) { summary.pass++; return; }
      summary.fail++;
      summary.failures.push(detail ? name + " — " + detail : name);
    },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(summary)); return summary; }
  };
}

export function main(args) {
  var h = mkHarness();
  var c = createClient({ apiBaseUrl: args.apiBaseUrl, spaceId: args.spaceId, noTrace: true });
  var uniq = "" + new Date().getTime();

  // --- createType with properties (xKey + kind) ---------------------------
  var movieType = "Movie_" + uniq;
  var ct = c.createType({
    name: movieType,
    properties: [
      { key: "title", name: "Title", format: "text" },
      { key: "year", name: "Year", format: "number" }
    ]
  });
  h.check("createType ok", ct && ct.ok, JSON.stringify(ct));

  // --- createObject with type + properties (bare keys, single type) -------
  var co = c.createObject(movieType, {
    name: "Casablanca",
    properties: { "title": "Casablanca", "year": 1942 }
  });
  h.check("createObject ok", co && co.ok, JSON.stringify(co));
  var objId = co && co.id;
  h.check("createObject returns id", !!objId);

  // --- getObject: readable nested record ----------------------------------
  var obj = c.getObject(objId);
  h.check("getObject name hoisted", obj && obj.name === "Casablanca", obj && obj.name);
  h.check("getObject nested title", getProp(obj, movieType + ".title") === "Casablanca",
    JSON.stringify(obj && obj[movieType]));
  h.check("getObject nested year (number)", getProp(obj, movieType + ".year") === 1942,
    JSON.stringify(getProp(obj, movieType + ".year")));
  h.check("getObject keeps any.types", obj && obj.any && Array.isArray(obj.any.types));

  // --- updateObject: dotted property write --------------------------------
  var up = c.updateObject(objId, { properties: { } });
  // empty is a no-op success
  h.check("updateObject empty props ok", up && up.ok, JSON.stringify(up));

  var up3 = c.updateObject(objId, { properties: dotted(movieType, { title: "Casablanca (1942)", year: 1943 }) });
  h.check("updateObject dotted ok", up3 && up3.ok, JSON.stringify(up3));
  var obj2 = c.getObject(objId);
  h.check("update applied title", getProp(obj2, movieType + ".title") === "Casablanca (1942)",
    getProp(obj2, movieType + ".title"));
  h.check("update applied year", getProp(obj2, movieType + ".year") === 1943,
    "" + getProp(obj2, movieType + ".year"));

  // --- validation surfaces: unknown prop ----------------------------------
  var bad = c.updateObject(objId, { properties: dotted(movieType, { nope: "x" }) });
  h.check("unknown prop rejected (not ok)", bad && bad.ok === false, JSON.stringify(bad));

  // --- validation surfaces: kind mismatch ---------------------------------
  var badKind = c.updateObject(objId, { properties: dotted(movieType, { year: "not a number" }) });
  h.check("kind mismatch rejected (not ok)", badKind && badKind.ok === false, JSON.stringify(badKind));

  // --- getObjects by type returns the object with nested props ------------
  var list = c.getObjects(movieType);
  h.check("getObjects returns array", Array.isArray(list));
  var found = null;
  for (var i = 0; i < list.length; i++) { if (list[i].id === objId) { found = list[i]; break; } }
  h.check("getObjects finds our object", !!found);
  h.check("getObjects nested prop readable", found && getProp(found, movieType + ".title") === "Casablanca (1942)",
    found && JSON.stringify(found[movieType]));

  // --- true multitype: one object carrying two user types -----------------
  var comicType = "ComicBook_" + uniq;
  var ctc = c.createType({
    name: comicType,
    properties: [{ key: "issue", name: "Issue", format: "number" }]
  });
  h.check("createType comic ok", ctc && ctc.ok, JSON.stringify(ctc));

  var multi = c.createObject(movieType, {
    name: "Crossover",
    types: [comicType],
    properties: merge(dotted(movieType, { title: "The Film" }), dotted(comicType, { issue: 7 }))
  });
  h.check("multitype createObject ok", multi && multi.ok, JSON.stringify(multi));
  var mObj = c.getObject(multi.id);
  h.check("multitype carries both types",
    mObj && mObj.any && mObj.any.types.indexOf(_id(c, movieType)) !== -1 &&
    mObj.any.types.indexOf(_id(c, comicType)) !== -1,
    JSON.stringify(mObj && mObj.any && mObj.any.types));
  h.check("multitype reads Movie.title", getProp(mObj, movieType + ".title") === "The Film",
    getProp(mObj, movieType + ".title"));
  h.check("multitype reads ComicBook.issue", getProp(mObj, comicType + ".issue") === 7,
    "" + getProp(mObj, comicType + ".issue"));

  // dotted update hitting both type namespaces in one call
  var bothUpd = merge(dotted(movieType, { title: "The Film v2" }), dotted(comicType, { issue: 8 }));
  var ub = c.updateObject(multi.id, { properties: bothUpd });
  h.check("multitype dotted update ok", ub && ub.ok, JSON.stringify(ub));
  var mObj2 = c.getObject(multi.id);
  h.check("multitype update Movie.title", getProp(mObj2, movieType + ".title") === "The Film v2");
  h.check("multitype update ComicBook.issue", getProp(mObj2, comicType + ".issue") === 8);

  return h.done();
}

// _id resolves a type name to its id via a throwaway getObjects call's catalog —
// here we just read it off the type list to keep the test self-contained.
function _id(c, typeName) {
  var types = c.getTypes();
  for (var i = 0; i < types.length; i++) { if (types[i].name === typeName) return types[i].id; }
  return null;
}

function merge(a, b) {
  var out = {};
  for (var k in a) { if (Object.prototype.hasOwnProperty.call(a, k)) out[k] = a[k]; }
  for (var k2 in b) { if (Object.prototype.hasOwnProperty.call(b, k2)) out[k2] = b[k2]; }
  return out;
}

// dotted builds a { "Type.prop": value } map from a type name + plain object.
function dotted(typeName, kv) {
  var out = {};
  for (var k in kv) { if (Object.prototype.hasOwnProperty.call(kv, k)) out[typeName + "." + k] = kv[k]; }
  return out;
}
