---
title: Subagents
description: Delegate a self-contained task to a quiet child loop that shares the space and tools but starts with fresh context, and get its report back as a value.
order: 40
---
# Subagents

Delegate a bounded task when it needs its own working context and should return a report to the parent. `subagent@v1.delegate(space, task, opts=None)` calls the same conversation loop in **quiet mode**, then returns its final reply as a value.

The child starts from the supplied task text, without the parent's history or recalled memories. Delegation is blocking and sequential; it does not start a persistent background worker. The table below lists the context, tool, and trace behavior. The guest-code example assumes the `agent` overlay and model tier are configured.

## When to delegate

Delegate when intermediate steps would only clutter the parent's context — a survey over many objects, a batch transformation, a research errand. Write the task like a good ticket: goal, inputs (ids, names), and what the report must contain. The child starts blank and knows nothing of the parent conversation.

```python
sa = use("agent:subagent@v1")
out = sa.delegate(space,
    "Survey every object of type `task` with status open in space 'dev'. "
    "Report: count, the five oldest by createdAt (id, name, age in days), "
    "and any task with no assignee.")
out["report"]   # the child's final text
out["stop"]     # done | wrapup | ...
out["turns"], out["tokens"]
```

## What the child gets

| Aspect | Child loop |
|---|---|
| Space | the same space as the parent — same types, objects, programs |
| Tools | the full tool surface; system prompt composed from the same skills without the `_soul` identity, plus a section saying it is a subagent whose reply returns to the delegating agent |
| Context | only the task text — no chat history, no memory injection |
| Chat | cannot post; interim text is not surfaced |
| Ceilings | bounded like any run — `maxTurns` 30 by default; `opts` takes `maxTurns`, `maxTokensTotal`, `tier`, `agentName` |
| Persistence | no `agent_turns` record, no ROI log |
| Trace | the child's model calls and cells nest inside the parent's trace under the `subagent.delegate` span |

Delegation is a thin wrapper, not a second loop implementation — `use("toolcaller@v1").main({..., "quiet": True})`. It is blocking and sequential; parallel children are not supported.

## Known limits

- Child cells share the kernel namespace with parent cells (`subcell` is reentrant by design). Cell ids stay distinct because tool-call ids are provider-unique.
- Depth beyond one level is mechanically possible and prompt-discouraged.

> **Note.** Because the child does not drain the mailbox, a `break` or `inject` sent during a delegation is seen by the parent on its next turn, after the child returns.

For a search-and-synthesise errand over the web rather than the space, use `deepResearch@v1` instead — it writes its findings as pages. See [Tools](tools.html).
