#!/usr/bin/env bash
# Claude Code PreToolUse hook — enforce that /simplify ran before a commit.
#
# Registered on the Bash tool in .claude/settings.json. A git commit is BLOCKED
# (permissionDecision=deny) until the /simplify skill has been run on the exact set
# of staged changes. Approval is recorded in a marker file keyed to the staged-diff
# hash, so the gate re-arms automatically whenever the staged content changes.
#
# Bypass: `git commit --no-verify` (emergencies only).
set -euo pipefail

input="$(cat)"

if command -v jq >/dev/null 2>&1; then
  cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // ""')"
else
  cmd="$(printf '%s' "$input" | grep -oE '"command"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n1 | sed -E 's/.*:[[:space:]]*"(.*)"/\1/')"
fi

# Only gate real `git commit` invocations.
if ! printf '%s' "$cmd" | grep -qE '(^|[^[:alnum:]_])git[[:space:]]+(-[^[:space:]]+[[:space:]]+)*commit($|[[:space:]])'; then
  exit 0
fi
case "$cmd" in
  *--help*|*' -h'*|*--no-verify*) exit 0 ;;  # help / explicit bypass
esac

project_dir="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
cd "$project_dir" || exit 0

git_dir="$(git rev-parse --git-dir 2>/dev/null || echo .git)"
marker="$git_dir/claude-simplify-ok"
staged_hash="$(git diff --cached | sha1sum | cut -d' ' -f1)"

# Already approved for this exact staged content → let the commit through.
if [[ -f "$marker" && "$(cat "$marker")" == "$staged_hash" ]]; then
  exit 0
fi

reason="STOP — run /simplify before committing.

1. Run the /simplify skill on the staged changes (it reviews for reuse,
   simplification, efficiency and applies fixes).
2. Re-stage any files /simplify edits (git add -u).
3. Refresh PROGRESS.md (\"Reste à faire\" / \"État actuel\") if the change moved the
   project forward.
4. Record approval for the current staged set, then re-run the commit:
     git diff --cached | sha1sum | cut -d' ' -f1 > \"$marker\"

The commit is blocked until step 4's marker matches the staged diff."

jq -cn --arg r "$reason" '{
  hookSpecificOutput: {
    hookEventName: "PreToolUse",
    permissionDecision: "deny",
    permissionDecisionReason: $r
  }
}'
exit 0
