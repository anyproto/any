// _estimateCost — Anthropic per-turn cost estimation for the debug collector
// (toolcall_core@v1). Pure unit test, no server.

import { _estimateCost } from "toolcall_core@v1";

function mkHarness() {
  var s = { pass: 0, fail: 0, failures: [] };
  return {
    check: function (n, c, d) { if (c) s.pass++; else { s.fail++; s.failures.push(d ? n + " — " + d : n); } },
    done: function () { console.log("HARNESS_RESULT " + JSON.stringify(s)); return s; }
  };
}
var h = mkHarness();

function approx(a, b) { return Math.abs(a - b) < 1e-9; }

// Sonnet 4.6 ($3 in / $15 out): all four usage streams priced.
// 346 uncached + 2203 cache-write (1.25×) + 10528 cache-read (0.1×) + 121 out
// — the exact turn from the debug log that showed cost: null.
var c = _estimateCost("claude-sonnet-4-6", {
  input_tokens: 346,
  cache_creation_input_tokens: 2203,
  cache_read_input_tokens: 10528,
  output_tokens: 121
});
var want = (346 * 3 + 2203 * 3 * 1.25 + 10528 * 3 * 0.1 + 121 * 15) / 1e6;
h.check("sonnet 4.6 with cache counters", approx(c, want), "got " + c + " want " + want);

// Longest-prefix match: dated haiku id resolves to the haiku price.
var ch = _estimateCost("claude-haiku-4-5-20251001", { input_tokens: 1000000, output_tokens: 0 });
h.check("dated haiku id matches", approx(ch, 1.0), "got " + ch);

// Opus tier prefix covers 4.6/4.7/4.8.
var co = _estimateCost("claude-opus-4-8", { input_tokens: 0, output_tokens: 1000000 });
h.check("opus 4.8 output price", approx(co, 25.0), "got " + co);

// Unknown model or OpenAI-shaped usage → null (never a wrong number).
h.check("unknown model → null", _estimateCost("gpt-5", { input_tokens: 10, output_tokens: 5 }) === null);
h.check("openai usage shape → null", _estimateCost("claude-sonnet-4-6", { prompt_tokens: 10, completion_tokens: 5 }) === null);
h.check("missing args → null", _estimateCost(null, null) === null);

h.done();
