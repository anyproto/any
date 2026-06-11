// JS-level integration test for anyHelper cross-space support: the `space`
// option as an arbitrary space id, catalog isolation between spaces, and
// the listSpaces / createSpace / getUIContext surface.
//
// Same contract as anyhelper_smoke_test.js: export main(args), print
// "HARNESS_RESULT " + JSON summary last.
//
// The second space is adopt-or-create by name ("crossspace_test") so
// repeated runs reuse one space instead of minting new ones.

import { createClient } from "anyHelper@v1";

function mkHarness() {
  var summary = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (name, cond, detail) {
      if (cond) { summary.pass++; return; }
      summary.fail++;
      summary.failures.push(detail ? name + " — " + detail : name);
    },
    done: function () {
      console.log("HARNESS_RESULT " + JSON.stringify(summary));
      return summary;
    }
  };
}

export function main(args) {
  var h = mkHarness();
  var c = createClient({ apiBaseUrl: args.apiBaseUrl, spaceId: args.spaceId, noTrace: true });

  // --- listSpaces ---
  var spaces = c.listSpaces();
  h.check("listSpaces returns rows", Array.isArray(spaces) && spaces.length > 0);
  var ownRow = null;
  for (var i = 0; i < spaces.length; i++) {
    if (spaces[i].id === args.spaceId) { ownRow = spaces[i]; break; }
  }
  h.check("own space is listed", !!ownRow, "spaceId " + args.spaceId + " not in listSpaces");

  // --- second space: adopt-or-create ---
  var OTHER = "crossspace_test";
  var otherId = null;
  for (var j = 0; j < spaces.length; j++) {
    if (spaces[j].name === OTHER && spaces[j].status === "active") { otherId = spaces[j].id; break; }
  }
  if (!otherId) {
    var created = c.createSpace(OTHER);
    h.check("createSpace", created.ok === true && !!created.id, JSON.stringify(created));
    otherId = created.id;
  } else {
    h.check("createSpace", true); // adopted an existing test space
  }
  if (!otherId) return h.done();

  // --- typed writes into the other space via the space option ---
  var t = c.createType({
    name: "Cross Item", xKey: "cross_item",
    properties: [{ key: "note", format: "text" }],
    space: otherId
  });
  h.check("createType in other space", t.ok === true, JSON.stringify(t));

  // Catalog isolation: the type lives in the other space's catalog only.
  h.check("type resolves in other space", typeof c.resolveType("cross_item", otherId) === "string");
  h.check("type absent from own-space catalog", c.resolveType("cross_item") === null);

  var stamp = "x" + new Date().getTime();
  var co = c.createObject("cross_item", {
    name: "cross-" + stamp, space: otherId,
    cross_item: { note: stamp }
  });
  h.check("createObject in other space", co.ok === true, JSON.stringify(co));

  var rows = c.getObjects({ type: "cross_item", filter: { "cross_item.note": stamp }, space: otherId });
  h.check("getObjects from other space", rows.length === 1, "got " + rows.length + " rows");
  h.check("read normalized by xKey", rows.length === 1 && rows[0].cross_item && rows[0].cross_item.note === stamp,
    rows.length === 1 ? JSON.stringify(rows[0]) : "no row");

  var upd = c.updateObject(co.id, { space: otherId, cross_item: { note: stamp + "-2" } });
  h.check("updateObject in other space", upd.ok === true, JSON.stringify(upd));
  var after = c.getObject(co.id, { space: otherId });
  h.check("update visible", after && after.cross_item && after.cross_item.note === stamp + "-2",
    JSON.stringify(after && after.cross_item));

  var del = c.deleteObject(co.id, { space: otherId });
  h.check("deleteObject in other space", del.ok === true, JSON.stringify(del));

  // --- getUIContext: simulate the any-ui contract in the agent's own space ---
  // (type `ui_context`, object named `ui-context`, props space_id /
  // object_id / view / updated_at — see any-ui docs/tasks/bobrik-view-context.md)
  var ut = c.createType({
    name: "UI Context", xKey: "ui_context",
    properties: [
      { key: "space_id", format: "text" },
      { key: "object_id", format: "text" },
      { key: "view", format: "text" },
      { key: "updated_at", format: "number" }
    ]
  });
  h.check("ui_context type", ut.ok === true, JSON.stringify(ut));

  var now = new Date().getTime();
  var ctxRows = c.getObjects("ui_context");
  var ctxId = ctxRows.length > 0 ? ctxRows[0].id : null;
  if (!ctxId) {
    var mk = c.createObject("ui_context", { name: "ui-context" });
    h.check("ui-context object created", mk.ok === true, JSON.stringify(mk));
    ctxId = mk.id;
  }
  var stampRes = c.updateObject(ctxId, {
    ui_context: { space_id: otherId, object_id: "obj-under-view", view: "object", updated_at: now }
  });
  h.check("ui-context stamped", stampRes.ok === true, JSON.stringify(stampRes));

  var got = c.getUIContext();
  h.check("getUIContext returns pointer", !!got, "null");
  h.check("getUIContext fields",
    !!got && got.spaceId === otherId && got.objectId === "obj-under-view" &&
    got.view === "object" && got.updatedAt === now,
    JSON.stringify(got));

  return h.done();
}
