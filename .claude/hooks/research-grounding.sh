#!/usr/bin/env bash
# Claude Code UserPromptSubmit hook — research-first reminder.
#
# Fires on every user prompt (UserPromptSubmit is matcher-less) and injects a reminder
# to ground non-trivial answers in current external sources (web SOTA, GitHub prior art,
# official docs) rather than memory. Full directive once per session, then a short nudge.
# Non-blocking (exit 0); the model sees additionalContext, the user does not.
set -euo pipefail

command -v jq >/dev/null 2>&1 || exit 0
input="$(cat)"
sid="$(printf '%s' "$input" | jq -r '.session_id // "nosession"')"
marker="${TMPDIR:-/tmp}/unpixel-research-${sid}"

if [[ -f "$marker" ]]; then
  ctx="Reminder (research-grounding): for any non-trivial/technical part of this request, check CURRENT external sources first (WebSearch/WebFetch for the state of the art, GitHub via scripts/ghx.sh for prior art, official docs) — THEN go further: look for improvements over the existing and out-of-the-box/novel approaches; don't just replicate prior art, improve on it. Prefer sources over memory and cite them. Skip for trivial/mechanical asks."
else
  : > "$marker" 2>/dev/null || true
  ctx="RESEARCH-FIRST, THEN IMPROVE (research-grounding skill) — for non-trivial questions or research in this project:
1. GROUND in current external sources: WebSearch/WebFetch for state-of-the-art and recent (2025-2026) best practices; GitHub via scripts/ghx.sh / gh for existing implementations, libraries, prior art; official docs (microsoft-docs MCP, claude-code-guide); deep-research skill for big questions.
2. GO BEYOND the existing: critique its limitations, find improvements (newer/faster/simpler approaches), and consider out-of-the-box / novel ideas — combine or surpass prior art rather than copying it.
3. Prefer up-to-date sources over memory, compare ≥2 alternatives, recommend the BEST approach (even if it's not the common one) with rationale, and cite what you used.
Calibrate: skip for trivial/mechanical asks."
fi

jq -cn --arg c "$ctx" '{hookSpecificOutput:{hookEventName:"UserPromptSubmit",additionalContext:$c}}'
