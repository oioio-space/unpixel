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

# Prefer rtk's compact gh; run under `mise exec` so gh resolves to the mise-managed
# version. Each branch degrades gracefully if mise or rtk is absent.
have() { command -v "$1" >/dev/null 2>&1; }

if have mise; then
  if have rtk; then exec mise exec -- rtk gh "$@"; fi
  exec mise exec -- gh "$@"
fi
if have rtk; then exec rtk gh "$@"; fi
exec gh "$@"
