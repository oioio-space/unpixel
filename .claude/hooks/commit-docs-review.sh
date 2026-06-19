#!/usr/bin/env bash
# Claude Code PreToolUse hook (docs + quality-tracking layer).
#
# On `git commit`, when the staged change is substantive (touches library/CLI Go
# code, not just tests/docs), this nudges Claude to keep the human-facing record
# in step with the evolution BEFORE the commit lands:
#   1. README.md + PROGRESS.md — reflect what this step changed (features/API/state).
#   2. benchmarks/quality-history.md — track decode quality + speed version-over-
#      version by running `mise run bench:panel:record` (appends one row, refreshes
#      the baseline) so each improvement is measured against the previous version.
#   3. benchmarks/HISTORY.md — when the hot path changed, the benchstat delta.
#
# Silent for non-commits and for commits that already stage these (or that touch
# no library/CLI code). The deterministic `.githooks/post-commit` still appends
# the commit line to PROGRESS history; this layer covers the narrative + panel.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

hook_is_git_commit "$(hook_cmd)" || exit 0

cd "$HOOK_PROJECT_DIR" 2>/dev/null || exit 0
staged="$(git diff --cached --name-only 2>/dev/null || true)"
[[ -n "$staged" ]] || exit 0

# Substantive = staged non-test Go under the library/CLI surface (or the root API).
code_changed=0
while IFS= read -r f; do
  case "$f" in
    *_test.go) continue ;;
    unpixel.go | internal/*.go | cmd/*.go | defaults/*.go | fonts/*.go)
      # In case patterns `*` matches `/` too, so internal/*.go covers nested dirs.
      code_changed=1 ;;
  esac
done <<< "$staged"
[[ $code_changed -eq 1 ]] || exit 0

grep -qxF 'README.md'   <<< "$staged" && readme_staged=1   || readme_staged=0
grep -qxF 'PROGRESS.md' <<< "$staged" && progress_staged=1 || progress_staged=0
grep -qxF 'benchmarks/quality-history.md' <<< "$staged" && panel_staged=1 || panel_staged=0

# Everything already in step → nothing to nudge.
[[ $readme_staged -eq 1 && $progress_staged -eq 1 && $panel_staged -eq 1 ]] && exit 0

todo=""
[[ $readme_staged   -eq 0 ]] && todo+="
- **README.md** — if this step changes a user-facing capability, flag, or the value story, update it (skill: readme-author). Skip if purely internal."
[[ $progress_staged -eq 0 ]] && todo+="
- **PROGRESS.md** — record what this step changed in État/Reste à faire (check off shipped items, note decisions). The post-commit hook only appends the commit line."
[[ $panel_staged    -eq 0 ]] && todo+="
- **Track the improvement across versions** — run \`mise run bench:panel:record\` (optionally \`PANEL_LABEL=<vX.Y.Z> ... -- \"<note>\"\`). It re-runs the recovery quality+speed panel over the fixture set, diffs it against the previous recorded version, refreshes benchmarks/quality-baseline.json, and appends a row to benchmarks/quality-history.md. Stage both."
todo+="
- **benchmarks/HISTORY.md** — only if a hot-path package changed: add the benchstat delta (\`mise run bench:compare\`)."

hook_emit_context "DOCS + QUALITY-TRACKING PRE-COMMIT REVIEW (substantive code is staged):

Keep the human-facing record in step with this step's evolution before committing:
$todo

Routing (token economy): the README/PROGRESS narrative → scribe sub-agent (Haiku); running
\`mise run bench:panel:record\` / bench:compare → quality-runner sub-agent (Haiku). A trivial
or purely-internal diff that changes nothing user-facing can be cleared inline — say so and proceed."
