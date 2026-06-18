#!/usr/bin/env bash
# Repo janitor — keep regenerable build/scan artifacts out of the repo.
#
#   --check  : pre-commit gate. Removes KNOWN untracked artifacts from the worktree,
#              then BLOCKS the commit if any staged file looks like an artifact.
#   (default): full local clean (`mise run clean`) — artifacts + go build cache.
#
# Only ever deletes paths that are NOT tracked by git, so it can never remove source.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# Regenerable artifact paths (all gitignored tool outputs).
artifacts=(dist coverage.out junit.xml sbom.cdx.json unpixel)
glob_artifacts=("*.test" "*.out" "*.exe" "coverage.*" "bench-*.txt" "*.prof")

remove_untracked() {
  local removed=()
  local p pat
  for p in "${artifacts[@]}"; do
    [[ -e "$p" ]] || continue
    git ls-files --error-unmatch "$p" >/dev/null 2>&1 && continue # tracked → never delete
    rm -rf -- "$p" && removed+=("$p")
  done
  shopt -s nullglob
  for pat in "${glob_artifacts[@]}"; do
    for p in $pat; do
      [[ -e "$p" ]] || continue
      git ls-files --error-unmatch "$p" >/dev/null 2>&1 && continue
      rm -f -- "$p" && removed+=("$p")
    done
  done
  shopt -u nullglob
  if ((${#removed[@]})); then
    printf '🧹 removed regenerable artifacts: %s\n' "${removed[*]}"
  fi
}

# Staged paths that must never be committed.
staged_offenders() {
  git diff --cached --name-only --diff-filter=ACM | grep -E \
    '(^|/)dist/|(^|/)coverage\.|(^|/)junit\.xml$|(^|/)sbom\.cdx\.json$|\.(test|out|exe)$|^unpixel$' || true
}

remove_untracked

if [[ "${1:-}" == "--check" ]]; then
  off="$(staged_offenders)"
  if [[ -n "$off" ]]; then
    printf 'These build/scan artifacts are staged and must not be committed:\n%s\n' "$off" >&2
    printf 'Unstage them (git restore --staged <f>) and ensure they are gitignored.\n' >&2
    exit 1
  fi
  echo "✓ No build/scan artifacts staged."
else
  go clean ./... 2>/dev/null || true
  echo "✓ Workspace cleaned."
fi
