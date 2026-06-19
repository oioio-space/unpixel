#!/usr/bin/env bash
# Recovery quality+speed panel over the committed fixture set.
#
#   scripts/bench-panel.sh            # run the panel, compare to the baseline
#   scripts/bench-panel.sh strict     # same, but FAIL on a decode-quality regression
#   scripts/bench-panel.sh record [note]  # promote to baseline + append a version
#                                          # row to benchmarks/quality-history.md
#                                          # (label via PANEL_LABEL, default HEAD)
#   scripts/bench-panel.sh bench      # benchstat-grade per-fixture speed (Go bench)
#
# Quality (exact-match rate, char accuracy, fidelity) and speed (per-fixture +
# total wall-clock) are printed each run next to the previous recorded panel
# (benchmarks/quality-baseline.json) so every improvement's effect is visible.
# The panel test lives behind the `panel` build tag (panel_test.go), so it never
# runs in the default test/coverage path.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

baseline="benchmarks/quality-baseline.json"
latest="benchmarks/quality-latest.json"
history="benchmarks/quality-history.md"

run_panel() { # $1 = extra env assignments string
  env PANEL_OUT="$latest" "$@" \
    go test -tags panel -run '^TestPanel$' -v -count=1 .
}

# append_history stamps the latest panel as one version row in quality-history.md,
# so decode quality + speed are tracked at every step. Label = $PANEL_LABEL, else
# the short HEAD; note = remaining args. Idempotent per (label) line is not
# enforced — each record is an intentional new version snapshot.
append_history() {
  local label note date row
  label="${PANEL_LABEL:-$(git rev-parse --short HEAD 2>/dev/null || echo '?')}"
  note="${*:-}"
  date="$(date +%F)"
  if [[ ! -f "$history" ]]; then
    # shellcheck disable=SC2016  # backticks are literal Markdown, not expansions
    {
      echo "# Recovery quality history"
      echo
      echo "Per-version decode quality + speed over the fixture panel, appended by"
      echo '`mise run bench:panel:record`. Quality is the headline; absolute ms vary by'
      echo "machine, so compare the **deltas** between rows. Raw run: \`quality-baseline.json\`."
      echo
      echo "| Date | Version | Exact | MeanAcc | Fidelity | Total ms | Note |"
      echo "|------|---------|-------|---------|----------|----------|------|"
    } > "$history"
  fi
  row="$(jq -r --arg d "$date" --arg v "$label" --arg n "$note" '
    .summary |
    "| \($d) | `\($v)` | \(.exact)/\(.n) (\((.exact_rate*100)|round)%) | " +
    "\((.mean_char_accuracy*1000|round)/1000) | \((.mean_fidelity*1000|round)/1000) | " +
    "\((.total_elapsed_ms|round)) | \($n) |"' "$latest")"
  printf '%s\n' "$row" >> "$history"
  git add -- "$history" "$baseline" 2>/dev/null || true
  echo "✓ history row appended: $history"
}

case "${1:-run}" in
  run)
    run_panel
    ;;
  strict)
    run_panel PANEL_STRICT=1
    ;;
  record)
    run_panel
    cp -f "$latest" "$baseline"
    echo "✓ baseline updated: $baseline (commit it to compare future runs against this version)"
    shift || true
    append_history "$@"
    ;;
  bench)
    go test -tags panel -run '^$' -bench '^BenchmarkPanel$' -benchmem -count "${BENCH_COUNT:-10}" .
    ;;
  *)
    echo "usage: bench-panel.sh [run|strict|record|bench]" >&2
    exit 2
    ;;
esac
