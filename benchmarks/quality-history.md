# Recovery quality history

Per-version decode quality + speed over the fixture panel, appended by
`mise run bench:panel:record`. Quality is the headline; absolute ms vary by
machine, so compare the **deltas** between rows. Raw run: `quality-baseline.json`.

| Date | Version | Exact | MeanAcc | Fidelity | Total ms | Note |
|------|---------|-------|---------|----------|----------|------|
| 2026-06-19 | `v0.4.0` | 14/14 (100%) | 1 | 1 | 1138 | panel introduced (baseline) |
| 2026-06-19 | `priors+pool` | 17/17 (100%) | 1 | 1 | 1784 | P3.7 secrets + P3.2 dictionary priors; P4.8 pooling; +3 secret fixtures |
| 2026-06-19 | `v0.5.0` | 17/17 (100%) | 1 | 1 | 1450 | P3.10 deblur, P3.11 auto-TopK, P4.11 intra-node parallel, +2 code fonts |
| 2026-06-21 | `v0.6.0` | 17/17 (100%) | 1 | 1 | 1503 | P6 blind bilingual (FR/EN) recovery + mosaictext zero-config monospace decoder |
