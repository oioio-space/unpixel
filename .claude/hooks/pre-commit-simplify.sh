#!/usr/bin/env bash
# Claude Code PreToolUse hook — enforce that /simplify ran before a commit.
# Blocks the commit until /simplify has run on the exact staged diff (approval is a
# marker file keyed to the staged-diff hash, so it re-arms whenever staging changes).
# Bypass: `git commit --no-verify`.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

cmd="$(hook_cmd)"
hook_is_git_commit "$cmd" || exit 0
case "$cmd" in *--no-verify*) exit 0 ;; esac # explicit bypass

cd "$HOOK_PROJECT_DIR" || exit 0
marker="$(git rev-parse --git-dir 2>/dev/null || echo .git)/claude-simplify-ok"
staged_hash="$(git diff --cached | sha1sum | cut -d' ' -f1)"

# Already approved for this exact staged content → let the commit through.
[[ -f "$marker" && "$(cat "$marker")" == "$staged_hash" ]] && exit 0

hook_emit_deny "STOP — run /simplify before committing.

1. Run the /simplify skill on the staged changes (reviews for reuse, simplification,
   efficiency and applies fixes).
2. Re-stage any files /simplify edits (git add -u).
3. Refresh PROGRESS.md (\"Reste à faire\" / \"État actuel\") if the change moved the
   project forward.
4. Record approval for the current staged set, then re-run the commit:
     git diff --cached | sha1sum | cut -d' ' -f1 > \"$marker\"

The commit is blocked until step 4's marker matches the staged diff."
