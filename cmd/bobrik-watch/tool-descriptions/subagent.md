## Tool Description

Delegate a self-contained subtask to a fresh sub-agent and get its reply as a string. Boots a full LLM loop in isolation — nothing is posted to chat, history is neither read nor written. Use only when the work would otherwise pollute the parent's context or trace volume; never recurse.

## Tool Schema

### delegate(text, opts?) [getter]

Run a self-contained sub-task and return its reply.

The sub-agent is a clean toolcall_core instance with the same tools, but no memory of this conversation — phrase `text` like a chat message to a fresh agent: goal, constraints, expected output shape. Its replies are captured and joined with `\n\n` instead of posted; chat history is bypassed in both directions; inner traces are dropped from the parent.

**Input:**
- `text` (string, required) — task description for the sub-agent.
- `opts` (object, optional)
    - `opts.version` (string, default `"v1"`) — toolcall_core version to invoke.
    - any other key is forwarded into the sub-agent's `args` (e.g. `botIdentity`). `text` and `__quiet` are reserved.

**Output:** `string` — the captured reply, or `"subagent error: ..."` on failure (never throws).

**When NOT to use:** anything you can do in a single `run_cell`. Boot cost is a few thousand tokens. Don't call `subagent.delegate` from inside another sub-agent invocation — the space's tool list won't stop you, but it just stacks loops.

**Examples:**

```js
var summary = subagent.delegate("List the top 3 most-used object types in this space. Return one line per type with name and approximate count.");

var review = subagent.delegate("Read the page named 'Q2 Plan' and check whether it still references the old onboarding funnel. Return YES or NO and a one-sentence justification.");
```
