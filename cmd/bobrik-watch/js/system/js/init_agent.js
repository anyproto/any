import { createClient } from "private:anyHelper@v1";
import { main as assistantMain } from "private:assistant@v5";

// init_agent — production entry point. Called by the host (bobrik-watch)
// on every incoming chat message via `private:init_agent@v1`.
//
// The agent runs in ONE space: its own private agent space (the "bao"
// space) — the same space that holds its chat, programs, skills, memory
// brain, and debug logs. Other spaces on the account are reached per-call
// via the `space` option on anyHelper methods (listSpaces / createSpace /
// getUIContext are the discovery surface). There is no separate "user
// space" and no cross-space credential forwarding — that was the old
// separate-account bobrik model (bot bootstrapped into foreign spaces via
// 1-1 chat), which `any` doesn't have.
//
// Responsibilities:
//   1. Ensure the agent-space types the system skills rely on exist
//      ("Pages" — _toolcaller tells the model to create pages with it;
//      "Space Context" — the _space_context singleton's type; "Agent
//      Skill" — skill objects). Idempotent — a cheap type-list probe
//      gates the work. ("Program", "Agent Debug Log", "Agent Log",
//      "Agent Memory" are server built-ins or Go-bootstrap-owned.)
//   2. Forward the call to private:assistant@v5.

function bootstrapTypes(client) {
  client.createType({
    name: "Agent Skill",
    properties: [
      { key: "agent_skill_name", format: "text" }
    ]
  });
  client.createType({ name: "Pages" });
  client.createType({ name: "Space Context" });
}

function _typesReady(client) {
  var needed = ["Agent Skill", "Pages", "Space Context"];
  var types = client.getTypes ? client.getTypes() : [];
  var names = {};
  for (var i = 0; i < types.length; i++) names[types[i].name] = true;
  for (var j = 0; j < needed.length; j++) {
    if (!names[needed[j]]) return false;
  }
  return true;
}

export function main(args) {
  if (!args) args = {};
  var apiBaseUrl    = args.apiBaseUrl    || env.ANYTYPE_API_URL;
  var apiKey        = args.apiKey        || env.ANYTYPE_API_KEY;
  var spaceId       = args.spaceId       || env.ANYTYPE_SPACE_ID;
  // Nav folder agent debug pages are filed under (host-injected). Empty
  // leaves them at root.
  var debugFolderId = args.debugFolderId || env.ANY_DEBUG_FOLDER_ID || "";

  if (!spaceId) return "Error: spaceId is required";

  var client = createClient({
    apiBaseUrl: apiBaseUrl,
    apiKey: apiKey,
    spaceId: spaceId,
    debugFolderId: debugFolderId,
    noTrace: true
  });

  if (!_typesReady(client)) {
    bootstrapTypes(client);
  }

  // Forward the full host args bag (identity, spaceType, chatId, etc.)
  // and override the resolved ids. A whitelist here would silently drop
  // host-supplied fields and break group-chat features.
  var runArgs = {};
  for (var k in args) {
    if (Object.prototype.hasOwnProperty.call(args, k)) runArgs[k] = args[k];
  }
  runArgs.apiBaseUrl    = apiBaseUrl;
  runArgs.apiKey        = apiKey;
  runArgs.spaceId       = spaceId;
  runArgs.debugFolderId = debugFolderId;

  return assistantMain(runArgs);
}
