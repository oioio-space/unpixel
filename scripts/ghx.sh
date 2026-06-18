#!/usr/bin/env bash
# GitHub CLI wrapper — the project-wide way to call gh.
#
#   - Always uses the mise-managed gh (pinned in mise.toml), not a stray system gh.
#   - Routes output through `rtk gh` (token-optimized, exit code preserved) when rtk
#     is available; otherwise calls gh directly.
#   - Degrades gracefully if mise or rtk are absent (never blocks on them).
#
# Usage: scripts/ghx.sh <gh args...>        e.g. scripts/ghx.sh pr list
#    or: mise run gh -- <gh args...>
set -uo pipefail

# Inner command: prefer rtk's compact gh, else plain gh (resolved from PATH).
# shellcheck disable=SC2016  # $@ is expanded by the inner `bash -c`, not here.
inner='if command -v rtk >/dev/null 2>&1; then exec rtk gh "$@"; else exec gh "$@"; fi'

if command -v mise >/dev/null 2>&1; then
  # Run under mise so `gh` resolves to the mise-managed version.
  exec mise exec -- bash -c "$inner" _ "$@"
fi
exec bash -c "$inner" _ "$@"
