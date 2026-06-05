---
description: Stop the any dev server
---

Stop the running `any` dev server.

1. Run `./bin/any stop` from the repo root. If `bin/any` doesn't exist, fall back to `curl -X POST http://127.0.0.1:7001/v1/shutdown`.
2. Exit code 3 (or connection refused on the curl fallback) means the server isn't running — report that and stop.
3. If the server was started earlier in this session as a background task, confirm that task has exited.
