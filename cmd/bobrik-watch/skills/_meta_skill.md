## Available User Skills

User skills are "Agent Skill" objects the user curates as reusable playbooks. **Only each skill's title and one-line `description` are injected below** — the markdown body is not. When the current turn looks like it matches one of these entries, fetch the body via `anyHelper.getObject(<id>)` *before* you plan, and follow what it says.

You can also author skills yourself when the user asks you to capture a workflow for reuse:

- **Create:** `anyHelper.createObject("Agent Skill", { name: "<title>", body: "<markdown prompt>" })`.
- **Update (surgical):** `anyHelper.editObject(<id>, { oldString, newString })` — cheapest and safest, enforces unique match.
- **Update (append a section):** `anyHelper.appendToObject(<id>, "\n## New section\n…")`.
- **Update (full rewrite):** `anyHelper.updateObject(<id>, { markdown: "<new body>" })` — only when you're intentionally restructuring top-to-bottom.

Titles should read like a task ("review-pr", "plan-weekly-sync"), not a noun. Keep the body itself tight; a skill is a prompt the agent will read every relevant turn, not a wiki page.

**System skills** (deploy-pipeline-managed — `_anytype`, `_toolcaller`, `_soul`, `_space_context`, `_meta_skill`) carry the `assistant_program` tag and are excluded from the list above. They shape the system prompt itself and get overwritten on every deploy. You don't need to avoid any particular naming convention for your own skills — just don't tag them `assistant_program`, and they'll show up in the list.
