#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI vulnerability-review layer).
#
# Registered on the Bash tool in .claude/settings.json. When the command is a
# `git commit`, it injects the vuln-guard checklist so Claude reviews the staged diff
# for logic-level security issues that gosec/govulncheck cannot catch. Non-blocking
# (the deterministic gosec + govulncheck gates are the hard block); silent otherwise.
set -euo pipefail

input="$(cat)"

if command -v jq >/dev/null 2>&1; then
  cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // ""')"
else
  cmd="$(printf '%s' "$input" | grep -oE '"command"[[:space:]]*:[[:space:]]*"[^"]*"' | head -n1 | sed -E 's/.*:[[:space:]]*"(.*)"/\1/')"
fi

if ! printf '%s' "$cmd" | grep -qE '(^|[^[:alnum:]_])git[[:space:]]+(-[^[:space:]]+[[:space:]]+)*commit($|[[:space:]])'; then
  exit 0
fi
case "$cmd" in
  *--help*|*' -h'*) exit 0 ;;
esac

context="SECURITY (VULN) PRE-COMMIT REVIEW (required before this commit):

Run \`git diff --cached\` and check the staged Go changes for:
- crypto: math/rand for security (use crypto/rand), MD5/SHA1, InsecureSkipVerify, weak TLS;
- injection: SQL/command/path built from input, os/exec with shell, text/template for HTML;
- authn/authz: missing checks, == for secret compare (use subtle.ConstantTimeCompare),
  unverified JWT, predictable tokens, auth bypass on error paths;
- DoS/memory: unbounded reads/allocs from input, ReadAll on untrusted streams;
- resources: missing defer Close(), TOCTOU, world-writable perms (0666/0777);
- leaks: internal errors/stack traces to clients, logging secrets/PII.

Fix findings before committing, or annotate a true false positive narrowly
(//nolint:gosec // reason). See the vuln-guard skill. (gosec + govulncheck also run
as hard gates; syft+grype scan the SBOM in CI.)"

jq -cn --arg ctx "$context" '{
  hookSpecificOutput: {
    hookEventName: "PreToolUse",
    additionalContext: $ctx
  }
}'
exit 0
