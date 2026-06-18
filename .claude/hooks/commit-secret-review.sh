#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI secret-review layer).
#
# Registered on the Bash tool in .claude/settings.json. When the command is a
# `git commit`, it injects the secret-guard checklist so Claude reviews the staged
# diff for sensitive data that regex scanners miss (PII, internal hosts, unknown
# token shapes). Non-blocking (the deterministic gitleaks gate is the hard block);
# silent for any other command.
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

context="SECRET-LEAK PRE-COMMIT REVIEW (required before this commit):

Run \`git diff --cached\` and confirm the staged changes contain NONE of:
- credentials/keys/tokens, passwords, Authorization headers, private keys, SSH keys,
  cloud creds (AWS AKIA…, GCP service-account JSON), DB strings with passwords, JWTs;
- local-only config: .env, *.local.*, kubeconfig, .netrc, .npmrc auth tokens;
- internal/PII: real internal hostnames/IPs, private endpoints, customer data, emails;
- accidental pastes: terminal output with tokens, debug dumps, build artifacts.

If anything sensitive is present, do NOT commit: remove it, move it to an env var /
secret manager, .gitignore it, and rotate it if it was ever real. See the
secret-guard skill for details. (gitleaks also runs as a hard gate.)"

jq -cn --arg ctx "$context" '{
  hookSpecificOutput: {
    hookEventName: "PreToolUse",
    additionalContext: $ctx
  }
}'
exit 0
