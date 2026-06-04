// Multitype property/type/record contract for the rewritten anyHelper.
//
// Pins the model resolved by probing the live v0.0.4 server:
//   - properties are stored/written by CID propId, namespaced per typeId
//   - GET /types/:id/properties maps propId <-> xKey <-> name (uniform for
//     builtin and user types)
//   - writes mirror reads: properties go in as nested type groups
//     ({ book: { author: "..." } }) and come back the same shape; unknown
//     top-level data keys ERROR rather than silently dropping the write.
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
  // The stable type handle for group keys is the xKey (slug of the name),
  // NOT the display name — records read back keyed by it.
  var mx = ct.type && ct.type.xKey;
  h.check("createType returns derived xKey", mx === "movie_" + uniq, "" + mx);

  // --- createObject with a nested type group (write what you read) --------
  var co = c.createObject(mx, grp(mx, { title: "Casablanca", year: 1942 }, { name: "Casablanca" }));
  h.check("createObject ok", co && co.ok, JSON.stringify(co));
  var objId = co && co.id;
  h.check("createObject returns id", !!objId);

  // --- getObject: readable nested record ----------------------------------
  var obj = c.getObject(objId);
  h.check("getObject name hoisted", obj && obj.name === "Casablanca", obj && obj.name);
  h.check("getObject nested title", getProp(obj, mx + ".title") === "Casablanca",
    JSON.stringify(obj && obj[movieType]));
  h.check("getObject nested year (number)", getProp(obj, mx + ".year") === 1942,
    JSON.stringify(getProp(obj, mx + ".year")));
  h.check("getObject keeps any.types", obj && obj.any && Array.isArray(obj.any.types));

  // --- updateObject: nested type-group write -------------------------------
  var up = c.updateObject(objId, {});
  // empty is a no-op success
  h.check("updateObject empty data ok", up && up.ok, JSON.stringify(up));

  var up3 = c.updateObject(objId, grp(mx, { title: "Casablanca (1942)", year: 1943 }));
  h.check("updateObject group ok", up3 && up3.ok, JSON.stringify(up3));
  var obj2 = c.getObject(objId);
  h.check("update applied title", getProp(obj2, mx + ".title") === "Casablanca (1942)",
    getProp(obj2, mx + ".title"));
  h.check("update applied year", getProp(obj2, mx + ".year") === 1943,
    "" + getProp(obj2, mx + ".year"));

  // --- validation surfaces: unknown prop ----------------------------------
  var bad = c.updateObject(objId, grp(mx, { nope: "x" }));
  h.check("unknown prop rejected (not ok)", bad && bad.ok === false, JSON.stringify(bad));

  // --- validation surfaces: kind mismatch ---------------------------------
  var badKind = c.updateObject(objId, grp(mx, { year: "not a number" }));
  h.check("kind mismatch rejected (not ok)", badKind && badKind.ok === false, JSON.stringify(badKind));

  // --- validation surfaces: misplaced property writes fail LOUD -----------
  // (a silently-dropped top-level key once lost a whole batch of writes)
  var dot = c.createObject(mx, grp(mx + ".title", "x", { name: "dotted" }));
  h.check("dotted top-level key rejected", dot && dot.ok === false && /nest property writes/.test(dot.error || ""), JSON.stringify(dot));

  var legacy = c.createObject(mx, { name: "legacy", properties: grp(mx, { title: "x" }) });
  h.check("data.properties rejected with hint", legacy && legacy.ok === false && /properties was removed/.test(legacy.error || ""), JSON.stringify(legacy));

  var stray = c.createObject(mx, grp("not_a_type_" + uniq, { title: "x" }, { name: "stray" }));
  h.check("unknown top-level key rejected", stray && stray.ok === false && /neither a data field/.test(stray.error || ""), JSON.stringify(stray));

  var notMap = c.createObject(mx, grp(mx, "not a map", { name: "notmap" }));
  h.check("non-object group rejected", notMap && notMap.ok === false && /must be a \{ prop: value \} object/.test(notMap.error || ""), JSON.stringify(notMap));

  var upStray = c.updateObject(objId, grp("not_a_type_" + uniq, { title: "x" }));
  h.check("updateObject unknown key rejected", upStray && upStray.ok === false && /neither a data field/.test(upStray.error || ""), JSON.stringify(upStray));

  // --- getObjects by type returns the object with nested props ------------
  var list = c.getObjects(mx);
  h.check("getObjects returns array", Array.isArray(list));
  var found = null;
  for (var i = 0; i < list.length; i++) { if (list[i].id === objId) { found = list[i]; break; } }
  h.check("getObjects finds our object", !!found);
  h.check("getObjects nested prop readable", found && getProp(found, mx + ".title") === "Casablanca (1942)",
    found && JSON.stringify(found[movieType]));

  // --- true multitype: one object carrying two user types -----------------
  var comicType = "ComicBook_" + uniq;
  var ctc = c.createType({
    name: comicType,
    properties: [{ key: "issue", name: "Issue", format: "number" }]
  });
  h.check("createType comic ok", ctc && ctc.ok, JSON.stringify(ctc));
  var cx = ctc.type && ctc.type.xKey;

  var multiData = grp(mx, { title: "The Film" }, { name: "Crossover", types: [cx] });
  multiData[cx] = { issue: 7 };
  var multi = c.createObject(mx, multiData);
  h.check("multitype createObject ok", multi && multi.ok, JSON.stringify(multi));
  var mObj = c.getObject(multi.id);
  h.check("multitype carries both types",
    mObj && mObj.any && mObj.any.types.indexOf(_id(c, movieType)) !== -1 &&
    mObj.any.types.indexOf(_id(c, comicType)) !== -1,
    JSON.stringify(mObj && mObj.any && mObj.any.types));
  h.check("multitype reads Movie.title", getProp(mObj, mx + ".title") === "The Film",
    getProp(mObj, mx + ".title"));
  h.check("multitype reads ComicBook.issue", getProp(mObj, cx + ".issue") === 7,
    "" + getProp(mObj, cx + ".issue"));

  // group update hitting both type namespaces in one call
  var bothUpd = grp(mx, { title: "The Film v2" });
  bothUpd[cx] = { issue: 8 };
  var ub = c.updateObject(multi.id, bothUpd);
  h.check("multitype group update ok", ub && ub.ok, JSON.stringify(ub));
  var mObj2 = c.getObject(multi.id);
  h.check("multitype update Movie.title", getProp(mObj2, mx + ".title") === "The Film v2");
  h.check("multitype update ComicBook.issue", getProp(mObj2, cx + ".issue") === 8);

  return h.done();
}

// _id resolves a type name to its id via a throwaway getObjects call's catalog —
// here we just read it off the type list to keep the test self-contained.
function _id(c, typeName) {
  var types = c.getTypes();
  for (var i = 0; i < types.length; i++) { if (types[i].name === typeName) return types[i].id; }
  return null;
}

// grp builds a { [typeKey]: group, ...extra } data object — type-group keys
// are dynamic (uniq-suffixed) in these tests.
function grp(typeKey, group, extra) {
  var out = {};
  out[typeKey] = group;
  if (extra) { for (var k in extra) { if (Object.prototype.hasOwnProperty.call(extra, k)) out[k] = extra[k]; } }
  return out;
}
