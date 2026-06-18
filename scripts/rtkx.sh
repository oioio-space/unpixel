#!/usr/bin/env bash
# Token-optimized command runner.
#
# Routes a command through `rtk err` (Rust Token Killer — shows only errors/warnings,
# compact on success) when rtk is available, otherwise runs the command unchanged.
#
# Why `rtk err` specifically: it PRESERVES the child's exit code (verified) and keeps
# error detail (e.g. golangci-lint violations) on failure, so it is safe to use inside
# the commit gate. `rtk test` is intentionally NOT used — it swallows the exit code.
#
# Never blocks: if rtk is missing, the command runs raw.
#
# Usage: scripts/rtkx.sh <cmd> [args...]
set -uo pipefail

if command -v rtk >/dev/null 2>&1; then
  exec rtk err "$@"
fi
exec "$@"
