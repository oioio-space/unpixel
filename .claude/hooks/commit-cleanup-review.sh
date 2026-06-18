#!/usr/bin/env bash
# Claude Code PreToolUse hook (AI repo-janitor layer).
# On `git commit`, reminds Claude to keep regenerable/stray artifacts out of the commit.
# Non-blocking (clean:check is the hard gate). Silent for any other command.
set -euo pipefail
# shellcheck source=lib/hooklib.sh
source "$(dirname "$0")/lib/hooklib.sh"

hook_is_git_commit "$(hook_cmd)" || exit 0

hook_emit_context "REPO-JANITOR PRE-COMMIT CHECK:

Run \`git diff --cached --name-only\` and make sure nothing gitignored/regenerable is
staged — build output (dist/, the unpixel binary, *.exe, *.test), test/scan output
(coverage.*, junit.xml, sbom.cdx.json, bench-*.txt, *.prof, *.out), or junk
(.DS_Store, mise.local.toml, .env*). The authoritative list is .gitignore.

If an artifact is staged, unstage it and gitignore it; for a new artifact kind, add it
to scripts/clean-artifacts.sh too. See the repo-janitor skill. (mise run clean:check
also runs as a hard gate.)

Routing (token economy): this is mechanical — beyond a glance, hand it to the quality-runner
sub-agent (Haiku, owns repo-janitor) rather than spending main-loop tokens."
