#!/usr/bin/env bash
# Claude Code PreToolUse hook — inject Go benchmark best-practices at the right moment.
#
# Registered on Write|Edit|MultiEdit in .claude/settings.json. When Claude writes a Go
# benchmark (a *.go file whose new content contains `func Benchmark`), it injects the
# go-benchmark guidance so the benchmark is correct and the optimization workflow is
# followed. Silent otherwise.
set -euo pipefail

input="$(cat)"

if ! command -v jq >/dev/null 2>&1; then
  exit 0
fi

fp="$(printf '%s' "$input" | jq -r '.tool_input.file_path // ""')"
case "$fp" in
  *.go) ;;
  *) exit 0 ;;
esac

# Gather written text across Write (.content), Edit (.new_string), MultiEdit (.edits[]).
written="$(printf '%s' "$input" | jq -r '
  [ .tool_input.content,
    .tool_input.new_string,
    ( .tool_input.edits[]?.new_string )
  ] | map(select(. != null)) | join("\n")' 2>/dev/null || echo "")"

# Only act when a benchmark function is being written.
printf '%s' "$written" | grep -qE 'func[[:space:]]+Benchmark[A-Z_]' || exit 0

context="GO BENCHMARK (go-benchmark skill) — you're writing a benchmark. Make it correct and
measure-driven:
- use \`for b.Loop()\` (Go 1.24+), not \`for i := 0; i < b.N; i++\`
- call \`b.ReportAllocs()\`; keep setup outside the loop (\`b.ResetTimer()\` if heavy)
- store results in a package-level \`sink\` var to defeat dead-code elimination
- table-driven sub-benchmarks via \`b.Run\`; \`b.SetBytes\` for throughput
Workflow: \`mise run bench:baseline\` → optimize → \`mise run bench:compare\` (benchstat) and keep
the change only if the gain is statistically significant with no allocs/other regression.
Full guide: .claude/skills/go-benchmark/SKILL.md"

jq -cn --arg ctx "$context" '{
  hookSpecificOutput: { hookEventName: "PreToolUse", additionalContext: $ctx }
}'
exit 0
