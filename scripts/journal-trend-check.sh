#!/usr/bin/env bash
# Journal trend check — guard the full-corpus quality record against silent
# regressions that the 17-fixture panel cannot see.
#
# It compares the two most recent rows of the "## Évolution" table in
# docs/JOURNAL.md (the per-corpus exact/total/≥70%/mean% record across all of
# testdata) and flags any backslide:
#   - exact-match count drops          -> REGRESSION  (hard fail, exit 1)
#   - ≥70%-similarity count drops      -> REGRESSION  (hard fail, exit 1)
#   - mean-similarity drops > threshold -> warning     (exit 0, or fail if STRICT=1)
#
# Why: mean% is noisy (±~2pp run-to-run) so it only warns by default; exact and
# ≥70% counts are the signal that actually matters and gate hard. This catches
# the class of regression that hid for 3 releases (real mean 11%→3% at v0.13.0,
# unseen because the panel only tracks testdata/fixtures).
#
#   scripts/journal-trend-check.sh            # report deltas; fail on exact/≥70% regression
#   MEAN_DROP_PP=4 scripts/journal-trend-check.sh
#   STRICT=1 scripts/journal-trend-check.sh   # also fail on a mean drop > MEAN_DROP_PP
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

journal="docs/JOURNAL.md"
mean_drop_pp="${MEAN_DROP_PP:-3}"
strict="${STRICT:-0}"

if [[ ! -f "$journal" ]]; then
  echo "journal-trend-check: $journal not found — nothing to check"
  exit 0
fi

awk -v drop="$mean_drop_pp" -v strict="$strict" '
  # Restrict parsing to the wide "## Évolution" table only; the exact-heading
  # anchor avoids re-entering on "## Évolution — décodeurs" and the run sections.
  /^## Évolution[ \t]*$/ { inevo = 1; next }
  inevo && /^## /        { inevo = 0 }
  inevo && /^\| 20[0-9][0-9]-/ { rows[++n] = $0 }

  END {
    if (n < 2) {
      printf "journal-trend-check: need >=2 evolution rows, have %d — skipping\n", n
      exit 0
    }
    split(rows[n - 1], prev, "|")
    split(rows[n],     cur,  "|")

    # Column labels in header order; data cells start at pipe-split index 5
    # (1="" 2=date 3=version 4=commit 5=cell1 ...).
    ncol = split("fix.zero fix.best blur.zero blur.best real.zero real.best " \
                 "wild.zero wild.best sick.zero sick.best ctx.C1a", lab, " ")

    prev[3] = trim(prev[3]); cur[3] = trim(cur[3])
    printf "Journal trend: %s -> %s\n", prev[3], cur[3]
    printf "%-11s  %-14s  %-14s  %s\n", "corpus", "prev ex/70/mn", "cur ex/70/mn", "verdict"

    fail = 0
    for (i = 1; i <= ncol; i++) {
      pc = prev[i + 4]; cc = cur[i + 4]
      gsub(/[ \t]/, "", pc); gsub(/[ \t]/, "", cc)
      if (pc == "" || cc == "" || pc == "—" || cc == "—") continue

      pe = fld(pc, "exact"); p7 = fld(pc, "ge70"); pm = fld(pc, "mean")
      ce = fld(cc, "exact"); c7 = fld(cc, "ge70"); cm = fld(cc, "mean")

      verdict = "ok"
      if (ce < pe) {
        verdict = sprintf("REGRESSION exact %d->%d", pe, ce); fail = 1
      } else if (c7 < p7) {
        verdict = sprintf("REGRESSION >=70%% %d->%d", p7, c7); fail = 1
      } else if (pm - cm > drop) {
        verdict = sprintf("warn mean -%dpp", pm - cm)
        if (strict == "1") { verdict = verdict " (STRICT fail)"; fail = 1 }
      } else if (cm > pm) {
        verdict = sprintf("up +%dpp", cm - pm)
      }
      printf "%-11s  %4d/%2d/%3d%%   %4d/%2d/%3d%%   %s\n", lab[i], pe, p7, pm, ce, c7, cm, verdict
    }

    if (fail) {
      print "TREND CHECK FAILED: a corpus regressed vs the previous journal run"
      exit 1
    }
    print "trend check ok"
  }

  # fld extracts exact (1st), ≥70% count (2nd-last) or mean% (last) from an
  # "exact/total/ge70/mean%" cell.
  function fld(cell, which,   a, k, s) {
    k = split(cell, a, "/")
    if (which == "exact") return a[1] + 0
    if (which == "ge70")  return a[k - 1] + 0
    s = a[k]; sub(/%$/, "", s); return s + 0
  }

  function trim(str) { gsub(/^[ \t]+|[ \t]+$/, "", str); return str }
' "$journal"
