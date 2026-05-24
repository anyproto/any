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
  client.createType({
    key: "any_program",
    name: "Program",
    plural_name: "Programs",
    icon: { name: "code-slash", color: "teal" },
    properties: [
      { key: "__any_program_name", format: "text" },
      { key: "__any_program_version", format: "text" }
    ]
  });
  client.createType({ key: "any_agent_debug", name: "Agent Debug Log", plural_name: "Agent Debug Logs", icon: { name: "bug", color: "orange" } });
  client.createType({
    key: "any_agent_skill",
    name: "Agent Skill",
    plural_name: "Agent Skills",
    icon: { name: "flash", color: "purple" },
    properties: [
      { key: "__any_agent_skill_name", format: "text" }
    ]
  });
}

// Cheap presence check: was bootstrapTypes ever run in this space?
// __any_agent_skill_name is the last property created by bootstrap, so its
// presence implies the full set of types is in place.
function _typesReady(client) {
  var props = client.getProperties();
  for (var i = 0; i < props.length; i++) {
    if (props[i].name === "__any_agent_skill_name") return true;
  }
  return false;
}

export function main(args) {
  if (!args) args = {};
  var apiBaseUrl    = args.apiBaseUrl    || env.ANYTYPE_API_URL;
  var apiKey        = args.apiKey        || env.ANYTYPE_API_KEY;
  var spaceId       = args.spaceId       || env.ANYTYPE_SPACE_ID;
  var systemSpaceId = args.sourceSpaceId || env.ANYTYPE_PRIVATE_SPACE_ID;

  if (!spaceId) return "Error: spaceId is required";

  var client = createClient({
    apiBaseUrl: apiBaseUrl,
    apiKey: apiKey,
    spaceId: spaceId,
    systemSpaceId: systemSpaceId,
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

  return assistantMain(runArgs);
}
