#!/usr/bin/env bash
# Code vulnerability gate: gosec (Go SAST) + govulncheck (reachable dep/stdlib CVEs).
# Output is intentionally NOT routed through rtk: for a security gate the specific
# finding (rule id, file, severity) must always be visible. gosec -quiet keeps clean
# runs short.
#
#   --staged : skip entirely when no Go files are staged (used by the pre-commit hook)
#   (default): scan the whole module (used by CI / on demand)
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if [[ "${1:-}" == "--staged" ]]; then
  if [[ -z "$(git diff --cached --name-only --diff-filter=ACM -- '*.go')" ]]; then
    echo "✓ No staged Go files — skipping code vuln scan."
    exit 0
  fi
fi

# gosec: static analysis for insecure Go patterns.
gosec -quiet ./...

# govulncheck: only reports vulnerabilities reachable from this module's code.
govulncheck ./...
