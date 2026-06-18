#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI style-review layer).
# On `git commit`, injects scripts/style-checklist.md so Claude reviews the staged
# diff against the Google Go style guide. Silent for any other command.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

hook_is_git_commit "$(hook_cmd)" || exit 0

checklist_file="$HOOK_PROJECT_DIR/scripts/style-checklist.md"
if [[ -f "$checklist_file" ]]; then
  checklist="$(cat "$checklist_file")"
else
  checklist="(style-checklist.md not found — review the diff against the go-style-guide skill references.)"
fi

hook_emit_context "GO STYLE-GUIDE PRE-COMMIT REVIEW (required before this commit):

$checklist

Run \`git diff --cached\` and verify the staged changes against every item above.
If any item is violated, fix it before committing.

Routing (token economy, no quality loss): for a substantial Go diff, delegate this review to
the go-reviewer sub-agent (Sonnet) instead of the main loop; clear a trivial diff inline."
