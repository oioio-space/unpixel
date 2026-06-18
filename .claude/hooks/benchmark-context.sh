#!/usr/bin/env bash
# Claude Code PreToolUse hook — inject Go benchmark best-practices at the right moment.
# Fires when Claude writes a Go benchmark (a *.go file whose new content contains
# `func Benchmark…`). Silent otherwise.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

case "$(hook_file_path)" in
  *.go) ;;
  *) exit 0 ;;
esac

hook_written | grep -qE 'func[[:space:]]+Benchmark[A-Z_]' || exit 0

hook_emit_context "GO BENCHMARK (go-benchmark skill) — you're writing a benchmark. Make it correct and
measure-driven:
- use \`for b.Loop()\` (Go 1.24+), not \`for i := 0; i < b.N; i++\`
- call \`b.ReportAllocs()\`; keep setup outside the loop (\`b.ResetTimer()\` if heavy)
- store results in a package-level \`sink\` var to defeat dead-code elimination
- table-driven sub-benchmarks via \`b.Run\`; \`b.SetBytes\` for throughput
Workflow: \`mise run bench:baseline\` → optimize → \`mise run bench:compare\` (benchstat) and keep
the change only if the gain is statistically significant with no allocs/other regression.
Full guide: .claude/skills/go-benchmark/SKILL.md"
