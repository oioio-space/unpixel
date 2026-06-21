#!/usr/bin/env bash
# gotest-caged.sh — run any go-test command inside a strict memory cgroup so a
# runaway test (currently the mosaictext leak: ~27 GB) is killed at the cage wall
# instead of OOM-freezing the machine.
#
# Why each knob (validated on Fedora 44 / systemd 259, cgroup v2, mem delegated):
#   MemoryMax=2G       hard wall — cgroup OOM-kills the offender here, contained.
#   MemorySwapMax=0    KEY anti-freeze: forbids spilling into zram-swap, which is
#                      what actually grinds Fedora to a halt before the OOM fires.
#   MemoryHigh=1500M   soft throttle: slows the process before the wall so the Go
#                      GC (see GOMEMLIMIT) gets a chance to reclaim a transient peak.
#   GOMEMLIMIT=1800MiB runtime-level soft cap: a genuine leak still dies, but a
#                      high-watermark spike survives via aggressive GC.
#   --collect          reap the transient scope even when the test fails.
#
# Usage:
#   scripts/gotest-caged.sh go test ./mosaictext/ -run TestDecode -v
#   scripts/gotest-caged.sh gotestsum --format pkgname -- ./...
#   MEM=4G scripts/gotest-caged.sh go test ./...     # override the wall
set -euo pipefail

mem="${MEM:-2G}"
high="${MEM_HIGH:-1500M}"
gomemlimit="${GOMEMLIMIT:-1800MiB}"

if [[ $# -eq 0 ]]; then
  echo "usage: $0 <test command...>" >&2
  exit 2
fi

exec systemd-run --user --scope --collect \
  -p MemoryMax="$mem" -p MemorySwapMax=0 -p MemoryHigh="$high" \
  -E GOMEMLIMIT="$gomemlimit" \
  -- "$@"
