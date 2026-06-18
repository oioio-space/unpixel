#!/usr/bin/env bash
# Absolute project rule: NO CGO (see CLAUDE.md). This deterministic gate fails if any
# package in the module uses cgo. It is wired into `mise run ci` and the pre-commit hook.
#
# Two independent signals:
#   1. `go list` reports CgoFiles for any of our packages (the authoritative source).
#   2. A defensive grep for the cgo import pseudo-package in tracked Go files.
# (Dependencies that need cgo are already caught separately: CGO_ENABLED=0 is pinned in
#  mise.toml [env], so a cgo-requiring dependency fails to build.)
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

fail=0

# 1) Authoritative: ask the toolchain which packages have cgo files.
cgo_pkgs="$(go list -f '{{if .CgoFiles}}{{.ImportPath}}{{end}}' ./... 2>/dev/null || true)"
if [[ -n "$cgo_pkgs" ]]; then
  printf '✗ CGO is forbidden — these packages use cgo:\n%s\n' "$cgo_pkgs" >&2
  fail=1
fi

# 2) Defensive: catch `import "C"` even before it compiles.
if grep -rEn '^[[:space:]]*import[[:space:]]+"C"|^[[:space:]]*"C"[[:space:]]*$' \
     --include='*.go' . 2>/dev/null | grep -v '/vendor/'; then
  printf '✗ CGO is forbidden — found an import "C" (cgo) above.\n' >&2
  fail=1
fi

if [[ $fail -ne 0 ]]; then
  printf '\nThis project is pure Go. Use a pure-Go library instead. See CLAUDE.md.\n' >&2
  exit 1
fi

echo "✓ no cgo: project is pure Go"
