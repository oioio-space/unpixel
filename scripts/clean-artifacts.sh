#!/usr/bin/env bash
# Repo janitor — keep regenerable build/scan artifacts out of the repo.
#
#   --check  : pre-commit gate. Removes KNOWN untracked artifacts from the worktree,
#              then BLOCKS the commit if any staged file is gitignored (an artifact).
#   (default): full local clean (`mise run clean`) — artifacts + go build cache.
#
# Only ever deletes paths that are NOT tracked by git, so it can never remove source.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# Regenerable artifacts to proactively delete (conservative explicit list — we do NOT
# `git clean -X` because that would also wipe local-only config like mise.local.toml).
# Globs are expanded here (nullglob drops non-matches); literal names stay as-is.
remove_untracked() {
  shopt -s nullglob
  local items=(dist coverage.* junit.xml sbom.cdx.json unpixel *.test *.out *.exe bench-*.txt *.prof)
  shopt -u nullglob
  local removed=() p
  for p in "${items[@]}"; do
    [[ -e "$p" ]] || continue
    git ls-files --error-unmatch "$p" >/dev/null 2>&1 && continue # tracked → never delete
    rm -rf -- "$p" && removed+=("$p")
  done
  ((${#removed[@]})) && printf '🧹 removed regenerable artifacts: %s\n' "${removed[*]}"
  return 0
}

remove_untracked

if [[ "${1:-}" == "--check" ]]; then
  # Authoritative artifact definition = .gitignore. Any staged file that matches an
  # ignore rule is an artifact that must not be committed (e.g. via `git add -f`).
  offenders="$(git diff --cached --name-only --diff-filter=ACM | git check-ignore --no-index --stdin 2>/dev/null || true)"
  if [[ -n "$offenders" ]]; then
    printf 'These gitignored artifacts are staged and must not be committed:\n%s\n' "$offenders" >&2
    printf 'Unstage them (git restore --staged <f>); they are already in .gitignore.\n' >&2
    exit 1
  fi
  echo "✓ No build/scan artifacts staged."
else
  go clean ./... 2>/dev/null || true
  echo "✓ Workspace cleaned."
fi
