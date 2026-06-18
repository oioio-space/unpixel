#!/usr/bin/env bash
# Go benchmark runner + A/B comparison with benchstat.
#   scripts/bench.sh            # run all benchmarks (-benchmem)
#   scripts/bench.sh baseline   # run + save bench-baseline.txt (BEFORE optimizing)
#   scripts/bench.sh compare    # run + benchstat vs bench-baseline.txt (AFTER optimizing)
#
# Artifacts (bench-*.txt) are gitignored and cleaned by repo-janitor.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

bench() { go test -run '^$' -bench=. -benchmem -count="${BENCH_COUNT:-6}" ./...; }

case "${1:-run}" in
  run)
    bench
    ;;
  baseline)
    bench | tee bench-baseline.txt
    echo "✓ baseline saved to bench-baseline.txt"
    ;;
  compare)
    if [[ ! -f bench-baseline.txt ]]; then
      echo "✗ no bench-baseline.txt — run 'mise run bench:baseline' before optimizing." >&2
      exit 1
    fi
    bench | tee bench-new.txt
    echo "── benchstat (baseline → new) ──"
    benchstat bench-baseline.txt bench-new.txt
    ;;
  *)
    echo "usage: bench.sh [run|baseline|compare]" >&2
    exit 2
    ;;
esac
