// Query surface: getObjects filter/sort/limit/offset over readable dotted
// keys, and getObjects dataset mode. Documents-as-tests for
// docs/09-query.md. Run via jsrunner_test.go.

import { createClient } from "anyHelper@v1";

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
  var T = "Film_" + uniq;
  // one-key object builder (computed keys aren't valid object literals in ES5)
  function one(k, v) { var o = {}; o[k] = v; return o; }

  // tx is the type xKey — the handle for type-args and dotted filter/sort keys.
  var tx = c.createType({ name: T, properties: [
    { key: "year", name: "Year", format: "number" },
    { key: "genre", name: "Genre", format: "text" }
  ]}).type.xKey;
  var yearKey = tx + ".year";
  var genreKey = tx + ".genre";
  function mk(name, year, genre) {
    var d = one(tx, { year: year, genre: genre });
    d.name = name + "_" + uniq;
    return c.createObject(tx, d);
  }
  mk("a", 1942, "drama"); mk("b", 1955, "drama"); mk("c", 1960, "noir"); mk("d", 1971, "noir");

  function names(list) { return list.map(function (x) { return x.name.replace("_" + uniq, ""); }).sort(); }
  function ordered(list) { return list.map(function (x) { return x.name.replace("_" + uniq, ""); }); }

  // getObjects returns the records ARRAY directly (no .records wrapper); it
  // THROWS on unknown type / server error rather than returning a flag.

  // comparison + AND
  var r1 = c.getObjects(tx, { filter: one(yearKey, { "$gte": 1955, "$lt": 1971 }) });
  h.check("$gte/$lt range", JSON.stringify(names(r1)) === JSON.stringify(["b", "c"]), JSON.stringify(names(r1)));

  // equality on string prop
  var r2 = c.getObjects(tx, { filter: one(genreKey, "noir") });
  h.check("equality filter", JSON.stringify(names(r2)) === JSON.stringify(["c", "d"]), JSON.stringify(names(r2)));

  // sort desc + limit
  var r3 = c.getObjects(tx, { sort: ["-" + yearKey], limit: 2 });
  h.check("sort desc + limit", JSON.stringify(ordered(r3)) === JSON.stringify(["d", "c"]), JSON.stringify(ordered(r3)));

  // offset (skip the top one)
  var r4 = c.getObjects(tx, { sort: ["-" + yearKey], limit: 2, offset: 1 });
  h.check("offset", JSON.stringify(ordered(r4)) === JSON.stringify(["c", "b"]), JSON.stringify(ordered(r4)));

  // $in on string
  var r5 = c.getObjects(tx, { filter: one(genreKey, { "$in": ["noir"] }) });
  h.check("$in", JSON.stringify(names(r5)) === JSON.stringify(["c", "d"]), JSON.stringify(names(r5)));

  // polymorphic first arg: object-form {type, filter} and string-only
  var r6 = c.getObjects({ type: tx, filter: one(genreKey, "drama") });
  h.check("object-form query", JSON.stringify(names(r6)) === JSON.stringify(["a", "b"]), JSON.stringify(names(r6)));
  var r7 = c.getObjects(tx);
  h.check("string-only form returns all of type", r7.length === 4, "" + r7.length);

  // unknown type throws (the message lists available types)
  var threw = false, msg = "";
  try { c.getObjects("no_such_type_" + uniq); } catch (e) { threw = true; msg = String(e.message || e); }
  h.check("unknown type throws", threw, msg);
  h.check("unknown-type error lists available types", threw && msg.indexOf("Available types:") !== -1, msg);

  // --- per-object dataset query (editor_blocks) ---
  var doc = c.createObject(tx, { name: "doc_" + uniq });
  var base = c.config.spacePath + "/objects/" + doc.id + "/editor/blocks";
  c.api("POST", base, { type: "paragraph", text: "first" });
  c.api("POST", base, { type: "paragraph", text: "second" });
  c.api("POST", base, { type: "paragraph", text: "third" });
  var blocks = c.getObjects({ objectId: doc.id, dataset: "editor_blocks", sort: ["nav.pos"] });
  h.check("dataset query returns 3 blocks", blocks.length === 3, "" + blocks.length);
  h.check("dataset query sorted by nav.pos", blocks[0].text === "first" && blocks[2].text === "third",
    JSON.stringify(blocks.map(function (b) { return b.text; })));

  // dataset query with limit
  var firstTwo = c.getObjects({ objectId: doc.id, dataset: "editor_blocks", sort: ["nav.pos"], limit: 2 });
  h.check("dataset limit", firstTwo.length === 2, "" + firstTwo.length);

  // deleteRecord completes dataset CRUD: drop the middle block.
  var del = c.deleteRecord(doc.id, "editor_blocks", blocks[1].id);
  h.check("deleteRecord ok", del && del.ok, JSON.stringify(del));
  var after = c.getObjects({ objectId: doc.id, dataset: "editor_blocks", sort: ["nav.pos"] });
  h.check("deleteRecord removed one block", after.length === 2, "" + after.length);
  h.check("deleteRecord removed the right one",
    after.map(function (b) { return b.text; }).indexOf("second") === -1,
    JSON.stringify(after.map(function (b) { return b.text; })));

  return h.done();
}
