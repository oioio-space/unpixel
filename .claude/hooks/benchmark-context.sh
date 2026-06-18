#!/usr/bin/env bash
# Claude Code PreToolUse hook — inject Go benchmark best-practices at the right moment.
# Two triggers (benchmarking is an absolute project rule, see CLAUDE.md):
#   1. Writing a Go benchmark (`func Benchmark…`) → full benchmark-writing guidance.
#   2. Editing a hot-path package (render/search/pixelate/metric/imutil) → a nudge to
#      prove any perf-affecting change with benchstat and keep benchmark coverage.
# Silent for everything else.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

fp="$(hook_file_path)"
case "$fp" in
  *.go) ;;
  *) exit 0 ;;
esac

# 1) Writing a benchmark — match `func Benchmark`, `func BenchmarkXxx(`, including the
#    valid empty-suffix `func Benchmark(`. (Previous regex missed the empty-suffix form.)
if hook_written | grep -qE 'func[[:space:]]+Benchmark([A-Z_][A-Za-z0-9_]*)?[[:space:]]*\('; then
  hook_emit_context "GO BENCHMARK (go-benchmark skill) — you're writing a benchmark. Make it correct and
measure-driven:
- use \`for b.Loop()\` (Go 1.24+), not \`for i := 0; i < b.N; i++\`
- call \`b.ReportAllocs()\`; keep setup outside the loop (\`b.ResetTimer()\` if heavy)
- store results in a package-level \`sink\` var to defeat dead-code elimination
- table-driven sub-benchmarks via \`b.Run\`; \`b.SetBytes\` for throughput
- compare with \`-count\` >= 10 so benchstat can find a statistically significant delta
Workflow: \`mise run bench:baseline\` → optimize → \`mise run bench:compare\` (benchstat) and keep
the change only if the gain is statistically significant with no allocs/other regression.
Full guide: .claude/skills/go-benchmark/SKILL.md"
  exit 0
fi

# 2) Editing hot-path production code (not a _test.go) — nudge to measure.
case "$fp" in
  *_test.go) exit 0 ;;
  */internal/render/* | */internal/search/* | */internal/pixelate/* | */internal/metric/* | */internal/imutil/*)
    hook_emit_context "PERF (go-benchmark skill — absolute project rule) — you're editing a hot-path package
(render/search/pixelate/metric/imutil), the per-candidate render→pixelate→distance→search core.
If this change can affect performance, PROVE it: \`mise run bench:baseline\` before, optimize, then
\`mise run bench:compare\` (benchstat, statistically significant, no alloc/throughput regression).
Keep these packages covered by \`func Benchmark…\` tests. Guide: .claude/skills/go-benchmark/SKILL.md"
    ;;
esac
exit 0
