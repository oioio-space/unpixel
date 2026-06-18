#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI repo-janitor layer).
#
# On `git commit`, injects the repo-janitor checklist so Claude double-checks that no
# regenerable/stray artifacts are being committed. Non-blocking (the deterministic
# clean gate is the hard block); silent for any other command.
set -euo pipefail

input="$(cat)"

if command -v jq >/dev/null 2>&1; then
  cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // ""')"
else
  cmd="$(printf '%s' "$input" | grep -oE '"command"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n1 | sed -E 's/.*:[[:space:]]*"(.*)"/\1/')"
fi

if ! printf '%s' "$cmd" | grep -qE '(^|[^[:alnum:]_])git[[:space:]]+(-[^[:space:]]+[[:space:]]+)*commit($|[[:space:]])'; then
  exit 0
fi
case "$cmd" in
  *--help*|*' -h'*) exit 0 ;;
esac

context="REPO-JANITOR PRE-COMMIT CHECK:

Make sure NO regenerable/stray artifacts are being committed:
- build: dist/, the unpixel binary, *.exe, *.test
- test/scan: coverage.out, junit.xml, sbom.cdx.json, *.out
- junk: .DS_Store, mise.local.toml, .env*

Run \`git diff --cached --name-only\` to confirm. If any artifact is staged, unstage it,
gitignore it, and (for a new artifact kind) add it to scripts/clean-artifacts.sh. See the
repo-janitor skill. (mise run clean:check also runs as a hard gate.)"

jq -cn --arg ctx "$context" '{
  hookSpecificOutput: { hookEventName: "PreToolUse", additionalContext: $ctx }
}'
exit 0
