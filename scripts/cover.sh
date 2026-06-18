#!/usr/bin/env bash
# Test coverage with an enforceable threshold.
#   scripts/cover.sh          # generate coverage.out + print total
#   scripts/cover.sh --check  # also fail if total < COVER_MIN (default 0)
#
# Raise COVER_MIN in mise.toml [env] as the port gains real test coverage.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

go test -covermode=atomic -coverprofile=coverage.out ./...

# Total % across the module (0.0 when there are no statements/tests yet).
total="$(go tool cover -func=coverage.out 2>/dev/null | awk '/^total:/{gsub(/%/,"",$3); print $3}')"
total="${total:-0.0}"
printf '\033[34mTotal coverage: %s%%\033[0m\n' "$total"

if [[ "${1:-}" == "--check" ]]; then
  min="${COVER_MIN:-0}"
  # Float compare via awk.
  if awk -v t="$total" -v m="$min" 'BEGIN{exit !(t+0 < m+0)}'; then
    printf '\033[31m✗ Coverage %s%% is below the required %s%%.\033[0m\n' "$total" "$min" >&2
    exit 1
  fi
  printf '\033[32m✓ Coverage %s%% meets the %s%% minimum.\033[0m\n' "$total" "$min"
fi
