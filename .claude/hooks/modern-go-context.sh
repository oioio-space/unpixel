#!/usr/bin/env bash
# Claude Code PreToolUse hook — reliably auto-applies the use-modern-go skill.
# On a Write/Edit of a *.go file, injects modern-Go idiom guidance for the project's
# Go version. Full rules once per session (marker keyed to session_id), then a nudge.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

case "$(hook_file_path)" in
  *.go) ;;
  *) exit 0 ;;
esac

ver="$(awk '/^go /{print $2; exit}' "$HOOK_PROJECT_DIR/go.mod" 2>/dev/null)"
ver="${ver:-unknown}"
sid="$(printf '%s' "$HOOK_INPUT" | jq -r '.session_id // "nosession"')"
marker="${TMPDIR:-/tmp}/unpixel-moderngo-${sid}"

if [[ -f "$marker" ]]; then
  hook_emit_context "Go ${ver} — apply use-modern-go idioms to this edit (slices/maps/cmp, min/max, range-over-int, t.Context, omitzero, wg.Go, new(val), errors.AsType). Avoid outdated patterns."
else
  : > "$marker" 2>/dev/null || true
  hook_emit_context "MODERN GO (use-modern-go skill) — this project targets Go ${ver}. Write modern idioms in this and subsequent Go edits:
- slices/maps/cmp packages, min/max, clear; range-over-int: for i := range n
- any (not interface{}); cmp.Or for defaults; errors.Is/As, errors.AsType[T] (1.26)
- tests: t.Context(); JSON: omitzero (not omitempty) for Duration/Time/struct/slice/map
- benchmarks: b.Loop(); goroutines: wg.Go(); iterate with strings.SplitSeq; new(val) for *T
Avoid outdated patterns when a modern equivalent exists.
Full reference: .claude/skills/use-modern-go/SKILL.md"
fi
