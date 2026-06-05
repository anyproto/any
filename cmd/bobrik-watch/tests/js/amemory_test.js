// amemory storage + category-filtering, post-migration (bare props under the
// "Agent Memory" type namespace, categories in the `tags` array). Exercises the
// non-LLM path: we write memory objects directly via anyHelper (fake vector
// hex, since loadAllMemories skips vector-less objects) and verify the read
// model + server-side array category filter. Run via jsrunner_test.go.

import { createClient, getProp } from "anyHelper@v1";
import { createAMemory, listCategories } from "amemory@v2";

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

  // createAMemory ensures the "Agent Memory" type + all bare props (incl tags).
  var amem = createAMemory(c);
  h.check("createAMemory returns api", amem && typeof amem === "object");

  var uniq = "" + new Date().getTime();
  // Helper: write a memory object the way addMemory does (minus the LLM bits).
  function mkMem(name, tags, context) {
    return c.createObject("agent_memory", {
      name: name + "_" + uniq,
      agent_memory: {
        vector: "abcd",         // fake hex so loaders don't skip it
        context: context,
        confidence: 7,
        tags: tags
      }
    });
  }
  var m1 = mkMem("pref", ["preference"], "likes dark mode");
  var m2 = mkMem("lesson", ["lesson", "food"], "salt before searing");
  h.check("create memory 1", m1 && m1.ok, JSON.stringify(m1));
  h.check("create memory 2", m2 && m2.ok, JSON.stringify(m2));

  // Read-model: nested props resolve by readable name.
  var o1 = c.getObject(m1.id);
  h.check("read context", getProp(o1, "agent_memory.context") === "likes dark mode", getProp(o1, "agent_memory.context"));
  h.check("read confidence", getProp(o1, "agent_memory.confidence") === 7, "" + getProp(o1, "agent_memory.confidence"));
  var t1 = getProp(o1, "agent_memory.tags");
  h.check("read tags array", Array.isArray(t1) && t1[0] === "preference", JSON.stringify(t1));

  // THE optimization: server-side category filter via the array `tags` field.
  var lessons = c.getObjects("agent_memory", { filter: { "agent_memory.tags": { "$in": ["lesson"] } } });
  var lessonNames = lessons.map(function (x) { return x.name; });
  h.check("array $in filter returns the lesson", lessonNames.indexOf("lesson_" + uniq) !== -1, JSON.stringify(lessonNames));
  h.check("array $in filter excludes the preference", lessonNames.indexOf("pref_" + uniq) === -1, JSON.stringify(lessonNames));

  // scalar-against-array = "contains"
  var foodies = c.getObjects("agent_memory", { filter: { "agent_memory.tags": "food" } });
  h.check("scalar contains filter finds food", foodies.some(function (x) { return x.name === "lesson_" + uniq; }), JSON.stringify(foodies.map(function (x) { return x.name; })));

  // listCategories (no LLM) reflects the migrated tag model.
  var cats = listCategories();
  h.check("listCategories returns object", cats && typeof cats === "object", JSON.stringify(cats));
  var observedNames = (cats && cats.observed) ? cats.observed.map(function (o) { return o.name || o; }) : [];
  h.check("listCategories observed includes preference", observedNames.indexOf("preference") !== -1, JSON.stringify(observedNames));
  h.check("listCategories observed includes lesson", observedNames.indexOf("lesson") !== -1, JSON.stringify(observedNames));

  return h.done();
}
