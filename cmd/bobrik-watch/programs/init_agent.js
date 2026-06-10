import { createClient } from "private:anyHelper@v1";
import { main as assistantMain } from "private:assistant@v5";

// init_agent — production entry point. Called by the anytype-heart middleware
// on every incoming message via `private:init_agent@v1`.
//
// Responsibilities:
//   1. Ensure the Anytype types the assistant relies on exist in the target
//      ("user") space. Idempotent — cheap getProperties probe gates the work.
//   2. Forward the call to private:assistant@v5 with cross-space credentials
//      (systemSpaceId = private) so anyHelper's program-dispatch falls
//      back to private when a name isn't in the user space.
//
// Assistant programs live ONLY in the private space — they reach user spaces
// via the runtime resolver's private-space fallback (unqualified imports) and
// anyHelper's cross-space dispatch (dynamic runProgram lookups). No
// per-space copy of system programs is maintained.
//
// Skills, memories, debug logs, and user-authored programs are space-local
// content and are not managed here. Skills get seeded by
// deploy-assistant.sh's --migrate / --deploy-all-spaces modes.

function bootstrapTypes(client) {
  // "Agent Debug Log", "Agent Log" (agent_turns/agent_chunks) and
  // "Agent Memory" (agent_memory_items on the brain object) are server
  // BUILT-IN types — registered at server boot, never created here. Minting
  // a user-space copy would collide on the xKey. (The runtime-created
  // "Agent Memory" property type the old amemory used is gone — see
  // docs/11-agent-memory.md.)
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
  var systemSpaceId = args.sourceSpaceId || env.ANYTYPE_PRIVATE_SPACE_ID;
  // Nav folder agent debug pages are filed under (host-injected). Empty
  // leaves them at root.
  var debugFolderId = args.debugFolderId || env.ANY_DEBUG_FOLDER_ID || "";

  if (!spaceId) return "Error: spaceId is required";

  var client = createClient({
    apiBaseUrl: apiBaseUrl,
    apiKey: apiKey,
    spaceId: spaceId,
    systemSpaceId: systemSpaceId,
    debugFolderId: debugFolderId,
    noTrace: true
  });

  if (!_typesReady(client)) {
    bootstrapTypes(client);
  }

  // Forward the full middleware args bag (identity, spaceType, chatId, etc.)
  // and override the resolved ids. A whitelist here would silently drop host-
  // supplied fields and break group-chat features.
  var runArgs = {};
  for (var k in args) {
    if (Object.prototype.hasOwnProperty.call(args, k)) runArgs[k] = args[k];
  }
  runArgs.apiBaseUrl    = apiBaseUrl;
  runArgs.apiKey        = apiKey;
  runArgs.spaceId       = spaceId;
  runArgs.systemSpaceId = systemSpaceId;
  runArgs.debugFolderId = debugFolderId;

  return assistantMain(runArgs);
}
