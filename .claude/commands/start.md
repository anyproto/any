---
description: Fetch, rebuild, and start the any dev server
---

Start the `any` dev server: update sources, rebuild, run. Execute these steps in order from the repo root.

## 1. Update this repo

Run `git fetch origin`.
=
- If on `main` with a clean working tree: `git merge --ff-only origin/main`.
- Otherwise (dirty tree, other branch, or ff not possible): report how many commits behind `origin/main` the checkout is and continue — build what's checked out.

## 3. Build

Run `make build` (swagger gen + `bin/any`). Stop and show the output if it fails.

## 4. Wallet check

Resolve the data dir: `$ANY_DATA_DIR` if set, else `~/.any`.

- If `<data-dir>/wallet.key` exists: nothing to do, the server picks it up.
- If missing: run `./bin/any init` and include the mnemonic verbatim in your reply so the user can save it.

## 5. Run the server

- If `./bin/any status` already reports a healthy server: report "already running" with the listen address and stop — never double-start.
- Otherwise launch `./bin/any run` as a background Bash task, then poll `./bin/any status` (up to ~10s) until it's healthy.
- On success: report the listen address. On failure: show the server output.
