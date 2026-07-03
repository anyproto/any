// _stampCacheTail — the moving prompt-cache breakpoint (toolcall_core@v1).
// Pure unit test, no server: the helper mutates a messages array in place.

import { _stampCacheTail } from "toolcall_core@v1";

function mkHarness() {
  var s = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (n, c, d) { if (c) s.pass++; else { s.fail++; s.failures.push(d ? n + " — " + d : n); } },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(s)); return s; }
  };
}
var h = mkHarness();

// String content converts to a stamped text block.
var m1 = [{ role: "user", content: "hello" }];
_stampCacheTail(m1);
h.check("string content becomes stamped block",
  Array.isArray(m1[0].content) &&
  m1[0].content[0].type === "text" &&
  m1[0].content[0].text === "hello" &&
  m1[0].content[0].cache_control && m1[0].content[0].cache_control.type === "ephemeral");

// The stamp MOVES: prior stamps are stripped, only the tail carries one.
var m2 = [
  { role: "user", content: [{ type: "text", text: "ask" }] },
  { role: "assistant", content: [{ type: "tool_use", id: "tu_1", name: "run_cell", input: { code: "1" } }] },
  { role: "user", content: [{ type: "tool_result", tool_use_id: "tu_1", content: "Output: 1" }] }
];
_stampCacheTail(m2);
h.check("tail tool_result stamped", !!m2[2].content[0].cache_control);
m2.push({ role: "assistant", content: [{ type: "tool_use", id: "tu_2", name: "run_cell", input: { code: "2" } }] });
m2.push({ role: "user", content: [{ type: "tool_result", tool_use_id: "tu_2", content: "Output: 2" }] });
_stampCacheTail(m2);
var stamped = 0;
for (var i = 0; i < m2.length; i++) {
  var c = m2[i].content;
  if (typeof c === "string") continue;
  for (var j = 0; j < c.length; j++) if (c[j].cache_control) stamped++;
}
h.check("exactly one stamp after advance", stamped === 1, "stamped=" + stamped);
h.check("stamp sits on the new tail", !!m2[4].content[0].cache_control);
h.check("old stamp removed", !m2[2].content[0].cache_control);

// Multi-block tail: stamp goes on the LAST block.
var m3 = [{ role: "user", content: [
  { type: "tool_result", tool_use_id: "a", content: "r1" },
  { type: "tool_result", tool_use_id: "b", content: "r2" }
] }];
_stampCacheTail(m3);
h.check("last block of multi-block tail", !m3[0].content[0].cache_control && !!m3[0].content[1].cache_control);

// Degenerate inputs don't throw and don't stamp empties.
_stampCacheTail([]);
_stampCacheTail(null);
var m4 = [{ role: "user", content: "" }];
_stampCacheTail(m4);
h.check("empty string content left alone", m4[0].content === "");

h.done();
