# 11 — Agent data

The agent's data — conversation turns, long-term memory, config,
secrets, triggers — is harness-owned userspace data: the anybao
harness declares it as runtime datasets (§ Runtime dataset schemas in
[`03-api.md`](03-api.md)) on objects it derives itself, and owns the
record shapes, validation, and search mappings end to end. The server
sees only generic datasets; nothing about the agent is compiled in.

The contract lives in the anybao repo — see its ADRs and `docs/` for
the dataset schemas and the layering model.
