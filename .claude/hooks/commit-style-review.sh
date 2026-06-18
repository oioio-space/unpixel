#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI style-review layer).
#
# Registered on the Bash tool in .claude/settings.json. On every Bash call it reads
# the tool-call JSON from stdin. If the command is a `git commit`, it returns
# additionalContext = the Google Go style checklist, forcing Claude to confront the
# staged diff against every (subjective) item before the commit runs. For any other
# command it stays silent and lets the call through.
set -euo pipefail

input="$(cat)"

# Extract the command being run (jq if available, else a tolerant grep fallback).
if command -v jq >/dev/null 2>&1; then
  cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // ""')"
else
  cmd="$(printf '%s' "$input" | grep -oE '"command"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n1 | sed -E 's/.*:[[:space:]]*"(.*)"/\1/')"
fi

# Only act on real `git commit` invocations (not `git commit --help`, not commit
# appearing inside an unrelated string).
if ! printf '%s' "$cmd" | grep -qE '(^|[^[:alnum:]_])git[[:space:]]+(-[^[:space:]]+[[:space:]]+)*commit($|[[:space:]])'; then
  exit 0
fi
case "$cmd" in
  *--help*|*' -h'*) exit 0 ;;
esac

# Resolve the checklist (single source of truth shared with the docs).
project_dir="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
checklist_file="$project_dir/scripts/style-checklist.md"

if [[ -f "$checklist_file" ]]; then
  checklist="$(cat "$checklist_file")"
else
  checklist="(style-checklist.md not found — review the diff against the go-style-guide skill references.)"
fi

context="GO STYLE-GUIDE PRE-COMMIT REVIEW (required before this commit):

$checklist

Run \`git diff --cached\` and verify the staged changes against every item above.
If any item is violated, fix it before committing."

# Emit the PreToolUse decision: inject context, allow the command to proceed.
jq -cn --arg ctx "$context" '{
  hookSpecificOutput: {
    hookEventName: "PreToolUse",
    additionalContext: $ctx
  }
}'
exit 0
