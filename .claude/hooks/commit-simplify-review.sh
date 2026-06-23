#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI /simplify-review layer).
# On `git commit`, injects the four-dimension cleanup checklist (reuse /
# simplification / efficiency / altitude) so Claude reviews the staged diff for
# quality, mirroring the sibling commit-*-review.sh hooks. The blocking gate
# (pre-commit-simplify.sh) makes ACTING on every dimension mandatory; this hook
# supplies the /simplify dimension's detailed checklist that the gate references
# but does not itself print. Non-blocking. Silent for any other command.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

hook_is_git_commit "$(hook_cmd)" || exit 0

hook_emit_context "/SIMPLIFY PRE-COMMIT REVIEW (required before this commit):

Run \`git diff --cached\` and review the staged changes across the four cleanup
dimensions. This is about improving the quality of the CHANGED code, not hunting
correctness bugs (that is commit-vuln-review / code-review). Fix what you find.

- REUSE: does new code re-implement something the repo already has? Grep adjacent
  packages (internal/imutil, internal/pixelate, internal/metric, internal/render,
  internal/lang, internal/segment) for an existing helper and call it instead of a
  fresh copy (e.g. ToRGBA, Crop, Lum601, block-mean, clamp/min/max, font advances).
- SIMPLIFICATION: redundant/derivable state, copy-paste with slight variation,
  deep nesting, dead code left behind. Name the simpler form that does the same job.
- EFFICIENCY: wasted work the diff adds — redundant recompute or I/O, allocations
  in inner loops, blocking work on a hot path. Name the cheaper alternative. For
  the hot-path packages (render/search/pixelate/metric/imutil/blinddecode/varfont)
  prove any perf-affecting change with benchstat — never by feel.
- ALTITUDE: is the change at the right depth, or a special-case bolted onto shared
  infra? Prefer generalizing the underlying mechanism over stacking special cases.
  But do NOT over-generalize genuinely-different operations into one helper.

For a SUBSTANTIAL diff, delegate to the go-reviewer sub-agent (Sonnet) and APPLY
its findings; a genuinely trivial diff may be cleared inline — per dimension, as a
deliberate judgement, not skipped. Re-stage anything the review edits (git add -u).

Routing (token economy, no quality loss): substantial diff → go-reviewer (review)
+ go-dev (apply); trivial diff cleared inline. See the /simplify skill."
