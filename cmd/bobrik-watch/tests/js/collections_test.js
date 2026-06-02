// Collections = nav folders (nav.type=2). Verifies createCollection sets
// nav.type via initialProperties (not the ignored top-level nav), and that
// addToCollection / getCollectionObjects / removeFromCollection work. Run via
// jsrunner_test.go.

import { createClient, getProp } from "anyHelper@v1";

function mkHarness() {
  var s = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (n, c, d) { if (c) s.pass++; else { s.fail++; s.failures.push(d ? n + " — " + d : n); } },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(s)); return s; }
  };
}

export function main(args) {
  var h = mkHarness();
  var c = createClient({ apiBaseUrl: args.apiBaseUrl, spaceId: args.spaceId, noTrace: true });
  var uniq = "" + new Date().getTime();

  var col = c.createCollection("Folder_" + uniq);
  h.check("createCollection ok", col && col.ok, JSON.stringify(col));

  // It must actually be a folder: nav.type === 2 (was silently 1 when nav went
  // top-level instead of under initialProperties).
  var folder = c.getObject(col.id);
  h.check("collection nav.type === 2", getProp(folder, "nav.type") === 2, "" + getProp(folder, "nav.type"));

  // Make a couple of objects and file them under the folder.
  var T = "Note_" + uniq;
  c.createType({ name: T });
  var a = c.createObject(T, { name: "a_" + uniq });
  var b = c.createObject(T, { name: "b_" + uniq });
  var add = c.addToCollection(col.id, [a.id, b.id]);
  h.check("addToCollection ok", add && add.ok, JSON.stringify(add));

  var kids = c.getCollectionObjects(col.id);
  var kidNames = kids.map(function (x) { return x.name; }).sort();
  h.check("getCollectionObjects returns both", kidNames.length === 2 && kidNames[0] === "a_" + uniq, JSON.stringify(kidNames));
  h.check("child nav.parentId points at folder", getProp(kids[0], "nav.parentId") === col.id, getProp(kids[0], "nav.parentId"));

  var rem = c.removeFromCollection(col.id, a.id);
  h.check("removeFromCollection ok", rem && rem.ok, JSON.stringify(rem));
  var kids2 = c.getCollectionObjects(col.id);
  h.check("one child left after remove", kids2.length === 1 && kids2[0].name === "b_" + uniq, JSON.stringify(kids2.map(function (x) { return x.name; })));

  return h.done();
}
