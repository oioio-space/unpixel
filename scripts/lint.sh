#!/usr/bin/env bash
# Deterministic Go style-guide gate.
# Runs gofmt, go vet, golangci-lint, build and tests. Used by the git pre-commit
# hook (.githooks/pre-commit) and runnable by hand: `./scripts/lint.sh`.
#
# By default it lints the whole module. Pass --staged to limit gofmt to the files
# staged for commit (used by the pre-commit hook).
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

# Make Go-installed tools (golangci-lint, gofumpt) reachable (fallback when not
# running under mise, which already puts the pinned tools on PATH).
gopath_bin="$(go env GOPATH)/bin"
export PATH="$PATH:$gopath_bin"

staged_only=0
[[ "${1:-}" == "--staged" ]] && staged_only=1

fail() { printf '\n\033[31m✗ %s\033[0m\n' "$1" >&2; exit 1; }
ok()   { printf '\033[32m✓ %s\033[0m\n' "$1"; }

# Collect Go files (staged set for the hook, else all tracked .go files).
if [[ $staged_only -eq 1 ]]; then
  mapfile -t go_files < <(git diff --cached --name-only --diff-filter=ACM -- '*.go')
else
  mapfile -t go_files < <(git ls-files -- '*.go')
fi

if [[ ${#go_files[@]} -eq 0 ]]; then
  ok "No Go files to check."
  exit 0
fi

# 1. Formatting — guide: Formatting (gofmt).
unformatted="$(gofmt -l "${go_files[@]}" || true)"
if [[ -n "$unformatted" ]]; then
  printf 'These files are not gofmt-clean:\n%s\n' "$unformatted" >&2
  fail "Formatting (run: gofmt -w . or gofumpt -w .)"
fi
ok "gofmt"

# 2. go vet — best-practices: correctness.
go vet ./... || fail "go vet found problems"
ok "go vet"

# 3. golangci-lint — the itemized style-guide linters (.golangci.yml).
if command -v golangci-lint >/dev/null 2>&1; then
  golangci-lint run ./... || fail "golangci-lint found style-guide violations"
  ok "golangci-lint"
else
  echo "⚠ golangci-lint not installed — skipping. Install:" >&2
  echo "    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest" >&2
fi

# 4. Build — code must compile.
go build ./... || fail "go build failed"
ok "go build"

# NOTE: tests are intentionally NOT run here. The dedicated `test:ci` task is the
# authoritative test + coverage gate (and `mise run ci` runs both lint and
# test:ci in parallel — running tests here too just duplicated the whole suite on
# every CI run and every commit). Run tests via `mise run test` / `mise run ci`.

printf '\n\033[32mAll style-guide checks passed.\033[0m\n'
