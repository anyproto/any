---
description: Stop the any dev server
---

Stop the running `any` dev server.

1. Run `./bin/any stop` from the repo root (pass `--data-dir` if the server runs on a non-default root). It finds the server by its held account lock and sends SIGTERM — no HTTP, and `POST /v1/shutdown` is refused on a standalone server.
2. Exit code 1 with "no running server under …" means nothing is running (an unauthorized server holds no account lock — stop it with Ctrl-C) — report that and stop.
3. If the server was started earlier in this session as a background task, confirm that task has exited.
