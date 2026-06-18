#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI vulnerability-review layer).
# On `git commit`, injects the vuln-guard checklist for logic-level security issues
# that gosec/govulncheck miss. Non-blocking. Silent for any other command.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

hook_is_git_commit "$(hook_cmd)" || exit 0

hook_emit_context "SECURITY (VULN) PRE-COMMIT REVIEW (required before this commit):

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
