#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI secret-review layer).
# On `git commit`, injects the secret-guard checklist for the staged diff. Non-blocking
# (gitleaks is the hard gate). Silent for any other command.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

hook_is_git_commit "$(hook_cmd)" || exit 0

hook_emit_context "SECRET-LEAK PRE-COMMIT REVIEW (required before this commit):

Run \`git diff --cached\` and confirm the staged changes contain NONE of:
- credentials/keys/tokens, passwords, Authorization headers, private keys, SSH keys,
  cloud creds (AWS AKIA…, GCP service-account JSON), DB strings with passwords, JWTs;
- local-only config: .env, *.local.*, kubeconfig, .netrc, .npmrc auth tokens;
- internal/PII: real internal hostnames/IPs, private endpoints, customer data, emails;
- accidental pastes: terminal output with tokens, debug dumps, build artifacts.

If anything sensitive is present, do NOT commit: remove it, move it to an env var /
secret manager, .gitignore it, and rotate it if it was ever real. See the
secret-guard skill for details. (gitleaks also runs as a hard gate.)

Routing (no quality loss on security): if a potential leak is ambiguous, delegate the
judgement to the security-auditor sub-agent (Opus); a clean trivial diff can be cleared inline."
