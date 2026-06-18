#!/usr/bin/env bash
# Point git at the versioned hooks directory so .githooks/pre-commit runs on commit.
# Run once after cloning: `./scripts/install-hooks.sh`.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

git config core.hooksPath .githooks
chmod +x .githooks/* scripts/*.sh .claude/hooks/*.sh 2>/dev/null || true

echo "✓ core.hooksPath set to .githooks — pre-commit style gate is active."
