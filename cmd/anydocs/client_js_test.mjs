// Run after `make docs`: exercise the downloadable client and subscription guide
// against mocked HTTP responses and streams, without starting an Any server.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import vm from "node:vm";
import test from "node:test";

const root = new URL("../../", import.meta.url);
const client = await readFile(new URL("website/dist/assets/examples/client.mjs", root), "utf8");
const guide = await readFile(new URL("website/05-realtime/subscribe.md", root), "utf8");
const blocks = [...guide.matchAll(/^```js\r?\n([\s\S]*?)^```\s*$/gm)];
assert.equal(blocks.length, 1, "subscription guide must contain one complete JavaScript example");
const startup = 'run(stop.signal).catch(e => { if (!stop.signal.aborted) console.error(e); });';
assert.ok(blocks[0][1].includes(startup), "locate the guide's startup call");
const examples = [
  { name: "downloadable client", source: client, account: "account-a", client: true },
  { name: "subscription guide", source: blocks[0][1].replace(startup, "globalThis.stopAnyDemo = () => stop.abort(); await run(stop.signal);"), account: "<accountId>", client: false },
];

const json = (data, status = 200) => new Response(JSON.stringify(data), {
  status, headers: { "content-type": "application/json" },
});
const frame = (event, data) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
const row = (id, name) => ({ id, any: { name }, modifiedAt: { $date: "2026-01-01T00:00:00Z" } });

async function runExample(example, scenario) {
  const logs = [], calls = [], streams = [];
  let authCalls = 0, subscriptionCalls = 0, timers = 0;
  let context;
  const cancel = () => context.stopAnyDemo();
  function stream(text, close = false) {
    const state = { cancelled: false, aborted: false };
    streams.push(state);
    state.response = new Response(new ReadableStream({
      start(controller) {
        state.abort = reason => { state.aborted = true; controller.error(reason); };
        controller.enqueue(new TextEncoder().encode(text));
        if (close) controller.close();
      },
      cancel() { state.cancelled = true; },
    }), { headers: { "content-type": "text/event-stream" } });
    return state.response;
  }
  context = vm.createContext({
    AbortController, DOMException, Response, ReadableStream, TextDecoderStream,
    // Keep native error constructors so instanceof checks match fetch/JSON errors.
    Error, SyntaxError, TypeError, JSON, URL, queueMicrotask,
    setTimeout(callback) {
      assert.ok(++timers < 12, "unexpected repeated reconnects");
      return setTimeout(callback, 0);
    },
    clearTimeout,
    console: Object.fromEntries(["log", "warn", "error"].map(level => [level, (...args) => {
      logs.push({ level, args });
      scenario.onLog?.({ level, args, cancel });
    }])),
    fetch: async (url, options = {}) => {
      const path = new URL(url).pathname;
      calls.push({ path, method: options.method ?? "GET" });
      if (path.endsWith("/auth")) {
        if (example.client && authCalls++ === 0) {
          return json(scenario.initialAuth ?? { authorized: true, accountId: example.account });
        }
        const answer = scenario.auth?.({ account: example.account }) ?? { authorized: true, accountId: example.account };
        return answer instanceof Response ? answer : json(answer);
      }
      if (path.endsWith("/subscribe")) {
        const response = scenario.subscribe({ attempt: ++subscriptionCalls, stream, cancel });
        const state = streams.find(stream => stream.response === response);
        if (state) options.signal.addEventListener("abort", () => state.abort(options.signal.reason), { once: true });
        return response;
      }
      if (path.endsWith("/spaces")) return json({ id: "space" });
      if (path.endsWith("/objects")) return json({ objectId: "object" });
      if (path.endsWith("/query")) return json({ total: 1, records: [row("object", "Reading list")] });
      if (path.endsWith("/set/any")) return json({});
      assert.fail(`unexpected request: ${path}`);
    },
  });
  let failure;
  try {
    await vm.runInContext(`(async () => {\n${example.source}\n})()`, context, {
      filename: example.client ? "client.mjs" : "subscribe.md",
    });
  } catch (error) { failure = error; }
  return { logs, calls, streams, subscriptionCalls, failure };
}

for (const example of examples) {
  test(`${example.name}: retries an HTML 502 and cancels the recovered stream`, { timeout: 2000 }, async () => {
    const result = await runExample(example, {
      subscribe: ({ attempt, stream, cancel }) => {
        if (attempt === 1) return new Response("<html>Bad Gateway</html>", { status: 502 });
        if (!example.client) setTimeout(cancel, 5);
        return stream(frame("snapshot", { records: [row("object", "Recovered")] }));
      },
      onLog: ({ args, cancel }) => {
        if (example.client && args[0].startsWith("Rename saved.")) cancel();
      },
    });
    assert.equal(result.failure, undefined);
    assert.equal(result.subscriptionCalls, 2);
    assert.ok(result.logs.some(log => log.level === "warn" && log.args.join(" ").includes("502")));
    assert.ok(result.streams[0].cancelled || result.streams[0].aborted);
  });

  test(`${example.name}: waits through unauthorized restart and uses a fresh snapshot`, { timeout: 2000 }, async () => {
    let checks = 0;
    const result = await runExample(example, {
      auth: ({ account }) => ++checks === 2 ? { authorized: false } : { authorized: true, accountId: account },
      subscribe: ({ attempt, stream, cancel }) => {
        if (attempt === 1) return stream(frame("snapshot", { records: [row("old", "Old")] }) +
          frame("closed", { reason: "server_shutdown" }));
        setTimeout(cancel, 5);
        return stream(frame("snapshot", { records: [row("new", "Recovered")] }));
      },
    });
    assert.equal(result.failure, undefined);
    assert.equal(checks, 3);
    assert.equal(result.subscriptionCalls, 2, "never subscribe while unauthorized");
    const views = result.logs.filter(log => log.level === "log" &&
      (example.client ? log.args[0] === "Live:" : log.args[0].includes("messages in the current window")));
    assert.ok(views.some(log => example.client ? log.args[1].length === 0 : log.args[0].startsWith("0 messages")), "clear the old account's data");
    const last = views.at(-1).args;
    assert.ok(example.client ? last[1].length === 1 && last[1][0] === "Recovered" : last[0].startsWith("1 messages"));
    assert.ok(result.streams.every(stream => stream.cancelled || stream.aborted), "close each stream");
  });

  test(`${example.name}: a deauthorized frame clears the view before auth recovers`, { timeout: 2000 }, async () => {
    let checks = 0, cleared = false;
    const result = await runExample(example, {
      auth: ({ account }) => {
        if (++checks === 2) assert.ok(cleared, "clear data before the next account check");
        return { authorized: true, accountId: account };
      },
      subscribe: ({ attempt, stream, cancel }) => {
        if (attempt === 1) return stream(frame("snapshot", { records: [row("old", "Old")] }) +
          frame("closed", { reason: "deauthorized" }));
        setTimeout(cancel, 5);
        return stream(frame("snapshot", { records: [] }));
      },
      onLog: ({ args }) => {
        if (example.client ? args[0] === "Live:" && args[1].length === 0 : args[0] === "0 messages in the current window") cleared = true;
      },
    });
    assert.equal(result.failure, undefined);
    assert.equal(result.subscriptionCalls, 2);
  });

  test(`${example.name}: stops and clears data when another account is authorized`, { timeout: 2000 }, async () => {
    let checks = 0;
    const result = await runExample(example, {
      auth: ({ account }) => ({ authorized: true, accountId: ++checks === 1 ? account : "different-account" }),
      subscribe: ({ stream }) => stream(frame("snapshot", { records: [row("old", "Old")] }) +
        frame("closed", { reason: "server_shutdown" })),
    });
    if (example.client) assert.match(result.failure?.message ?? "", /Account changed/);
    else assert.equal(result.failure, undefined);
    assert.equal(result.subscriptionCalls, 1);
    const last = result.logs.filter(log => log.level === "log").at(-1).args;
    assert.ok(example.client ? last[0] === "Live:" && last[1].length === 0 : last[0] === "0 messages in the current window");
  });

  test(`${example.name}: forbidden subscription is terminal`, { timeout: 2000 }, async () => {
    const result = await runExample(example, {
      subscribe: () => json({ error: { code: "acl.forbidden", message: "Access denied" } }, 403),
    });
    assert.equal(result.failure?.status, 403);
    assert.equal(result.subscriptionCalls, 1);
  });

  test(`${example.name}: malformed successful SSE is terminal`, { timeout: 2000 }, async () => {
    const result = await runExample(example, {
      subscribe: ({ stream }) => stream("event: snapshot\ndata: {broken}\n\n"),
    });
    assert.equal(result.failure?.name, "SyntaxError");
    assert.equal(result.subscriptionCalls, 1);
    assert.ok(result.streams[0].cancelled || result.streams[0].aborted);
  });
}

test("downloadable client: initial unauthorized account fails before creating data", { timeout: 2000 }, async () => {
  const result = await runExample(examples[0], {
    initialAuth: { authorized: false },
    subscribe: () => assert.fail("must not subscribe"),
  });
  assert.match(result.failure?.message ?? "", /Create or select an account/);
  assert.equal(result.calls.length, 1);
});
