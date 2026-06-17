// __main_source
// ui@v1 — drive the user's any-ui window. Thin wrapper over
// anyHelper.uiCommand (POST /v1/ui/commands): publishes a navigation
// directive to the account-wide, in-memory UI command channel, which the
// any-ui window subscribes to over SSE. Fire-and-forget — the reply's
// `subscribers` says how many UI windows received it (0 = nobody listening).
// Contract: docs/15-ui-commands.md.
//
// The command names its TARGET space/object in the payload, independent of the
// agent's own space — so the agent can open any space/object on the account.

import { createClient } from "anyHelper@v1";

function _newClient() {
  return createClient({
    apiBaseUrl: env.ANYTYPE_API_URL,
    apiKey: env.ANYTYPE_API_KEY,
    spaceId: env.ANYTYPE_SPACE_ID,
    noTrace: true
  });
}

// createUI(deps?) — factory, mainly for tests; inject a client.
export function createUI(deps) {
  deps = deps || {};
  var client = deps.client || _newClient();

  function openSpace(spaceId) {
    if (!spaceId) return { ok: false, error: "spaceId is required" };
    return client.uiCommand({ action: "open_space", spaceId: spaceId });
  }

  function openObject(spaceId, objectId) {
    if (!spaceId) return { ok: false, error: "spaceId is required" };
    if (!objectId) return { ok: false, error: "objectId is required" };
    return client.uiCommand({ action: "open_object", spaceId: spaceId, objectId: objectId });
  }

  return { openSpace: openSpace, openObject: openObject };
}

var _default = null;
function _getDefault() {
  if (!_default) _default = createUI();
  return _default;
}

// Tool-facing methods — lazily build the default instance so module import
// stays cheap (the boot prelude imports every tool).
export function openSpace(spaceId) {
  return _getDefault().openSpace(spaceId);
}

export function openObject(spaceId, objectId) {
  return _getDefault().openObject(spaceId, objectId);
}
