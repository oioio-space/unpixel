#!/usr/bin/env bash
# Claude Code PreToolUse hook — reliably auto-applies the use-modern-go skill.
#
# Registered on Write|Edit|MultiEdit in .claude/settings.json. Whenever Claude is
# about to write or edit a *.go file, this injects the modern-Go idiom directive (for
# the project's detected Go version) as additionalContext — so modern idioms are
# applied deterministically, not just when the skill happens to trigger.
#
# Token-aware: the full compact rule set is injected once per session; later Go edits
# in the same session get a one-line nudge. Silent for non-Go files.
set -euo pipefail

input="$(cat)"

if command -v jq >/dev/null 2>&1; then
  fp="$(printf '%s' "$input" | jq -r '.tool_input.file_path // ""')"
  sid="$(printf '%s' "$input" | jq -r '.session_id // "nosession"')"
else
  fp="$(printf '%s' "$input" | grep -oE '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n1 | sed -E 's/.*:[[:space:]]*"(.*)"/\1/')"
  sid="nosession"
fi

# Only act on Go source files.
case "$fp" in
  *.go) ;;
  *) exit 0 ;;
esac

# Detect the target Go version from go.mod (same logic as the use-modern-go skill).
proj="${CLAUDE_PROJECT_DIR:-.}"
ver="$(grep -rh "^go " --include="go.mod" "$proj" 2>/dev/null | cut -d' ' -f2 | sort | uniq -c | sort -nr | head -1 | xargs | cut -d' ' -f2 | grep . || echo unknown)"

marker="${TMPDIR:-/tmp}/unpixel-moderngo-${sid}"

if [[ -f "$marker" ]]; then
  context="Go ${ver} — apply use-modern-go idioms to this edit (slices/maps/cmp, min/max, range-over-int, t.Context, omitzero, wg.Go, new(val), errors.AsType). Avoid outdated patterns."
else
  : > "$marker" 2>/dev/null || true
  context="MODERN GO (use-modern-go skill) — this project targets Go ${ver}. Write modern idioms in this and subsequent Go edits:
- slices/maps/cmp packages, min/max, clear; range-over-int: for i := range n
- any (not interface{}); cmp.Or for defaults; errors.Is/As, errors.AsType[T] (1.26)
- tests: t.Context(); JSON: omitzero (not omitempty) for Duration/Time/struct/slice/map
- benchmarks: b.Loop(); goroutines: wg.Go(); iterate with strings.SplitSeq; new(val) for *T
Avoid outdated patterns when a modern equivalent exists.
Full reference: .claude/skills/use-modern-go/SKILL.md"
fi

jq -cn --arg ctx "$context" '{
  hookSpecificOutput: {
    hookEventName: "PreToolUse",
    additionalContext: $ctx
  }
}'
exit 0
