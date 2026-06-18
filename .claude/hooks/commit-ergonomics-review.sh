#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI helper-ergonomics layer).
# On `git commit`, if the staged diff touches exported Go API or the CLI, remind Claude
# to hunt for convenience/helper functions that make this human-facing project easy to
# use. Non-blocking (advisory). Silent for non-commit commands and for diffs with no Go.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

hook_is_git_commit "$(hook_cmd)" || exit 0

cd "$HOOK_PROJECT_DIR" 2>/dev/null || exit 0

# Only fire when staged Go changes plausibly touch the public surface: an exported
# declaration (func/type/const/var starting with a capital) or the CLI/flags. Keeps the
# nudge off pure-internal or non-Go commits.
staged_go="$(git diff --cached --name-only --diff-filter=ACM -- '*.go' 2>/dev/null || true)"
[[ -n "$staged_go" ]] || exit 0

diff_added="$(git diff --cached -- '*.go' 2>/dev/null | grep '^+' || true)"
if ! printf '%s' "$diff_added" | grep -qE '^\+[[:space:]]*(func|type|const|var)[[:space:]]+[A-Z]|cli\.|Flag\{|cmd/unpixel'; then
  # Also fire if a cmd/unpixel file is staged (CLI ergonomics) even without the above.
  printf '%s' "$staged_go" | grep -q '^cmd/unpixel/' || exit 0
fi

hook_emit_context "HELPER-ERGONOMICS PRE-COMMIT REVIEW (UnPixel is human-facing):

This commit changes exported API or the CLI. Before committing, walk the staged diff and ask, for
each new/changed exported symbol: \"What is the smallest program a human must write to use this —
and can a helper make it smaller?\"

- One-call wrapper for the common path (decode → wire defaults → run → best), so callers don't
  hand-assemble pieces or forget the \`defaults\` side-effect import.
- Broaden input: offer image.Image / io.Reader / file-path forms (Recover / RecoverReader /
  RecoverFile). Accept interfaces, return concrete types.
- Constructors with sensible defaults (useful zero values); functional options for the optional
  (WithCharset/WithWorkers…) instead of forcing a full Config literal — keep Config for power users.
- Don't make callers touch internals or follow a \"first X then Y\" sequence.
- Printable results (String()), and actionable error messages.
- CLI: sane defaults, - for stdin, examples in --help, machine-readable --json, clear exit codes.

Propose the EARNED helpers (1–3 high-value, not a dozen thin ones — least mechanism wins), with a
before/after; document them for GoDoc, test them, and keep the low-level API for power users. See
the helper-ergonomics skill. Routing: for a substantial change, delegate the review to the
go-reviewer sub-agent (Sonnet) and implement with go-dev; clear a tiny diff inline."
