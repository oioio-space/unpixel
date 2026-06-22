#!/usr/bin/env bash
# Claude Code PreToolUse hook — enforce the FULL pre-commit review before a commit.
# Blocks until every applicable review dimension has been addressed on the exact
# staged diff. Approval is ONE marker file keyed to the staged-diff hash, so it
# re-arms whenever staging changes; .githooks/post-commit also clears it so each
# new commit re-arms. The sibling commit-*-review.sh hooks inject each dimension's
# detailed checklist; this gate makes ACTING on them mandatory, not advisory —
# i.e. it covers /simplify AND secret/vuln/style/cleanup/ergonomics/docs alike.
# Bypass (use sparingly, e.g. a pure marker/config edit): `git commit --no-verify`.
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

hook_emit_deny "STOP — run the full pre-commit review before committing.

Address EVERY dimension below on the staged diff (the sibling commit-*-review hooks
print each checklist). For a SUBSTANTIAL diff, delegate to the routed sub-agent and
APPLY findings; a genuinely trivial diff may be cleared inline — but it must be a
deliberate judgement, per dimension, not skipped:
  • /simplify  — reuse / simplification / efficiency / altitude        (go-reviewer)
  • secrets    — no leaked credentials/PII                              (security-auditor if ambiguous)
  • vulns      — crypto/injection/authz/DoS/resources                  (security-auditor)
  • style      — Google Go style-guide items linters miss              (go-reviewer)
  • cleanup    — no regenerable artifacts staged                       (quality-runner)
  • ergonomics — earned helpers for new exported API/CLI               (go-reviewer)
  • docs       — README/PROGRESS + bench panel for user-facing changes (scribe)
Re-stage anything the review edits (git add -u). Then record ONE approval for the
current staged set and re-run the commit:
     git diff --cached | sha1sum | cut -d' ' -f1 > \"$marker\"

The commit stays blocked until the marker matches the staged diff. Record it ONLY
after genuinely completing the review — the marker attests the work was done."
