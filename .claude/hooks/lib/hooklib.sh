#!/usr/bin/env bash
# Shared helpers for Claude Code PreToolUse hooks. Source AFTER `set -euo pipefail`:
#   source "$(dirname "$0")/lib/hooklib.sh"
#
# Reads the hook payload from stdin once into HOOK_INPUT and exposes small helpers.
# jq is a pinned mise dependency; if it is somehow absent the hook no-ops (exit 0).

command -v jq >/dev/null 2>&1 || exit 0
HOOK_INPUT="$(cat)"
# Consumed by sourcing hooks, not within this lib.
# shellcheck disable=SC2034
HOOK_PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"

# The command of a Bash tool call (empty for other tools).
hook_cmd() { printf '%s' "$HOOK_INPUT" | jq -r '.tool_input.command // ""'; }

# The target path of a Write/Edit/MultiEdit call (empty otherwise).
hook_file_path() { printf '%s' "$HOOK_INPUT" | jq -r '.tool_input.file_path // ""'; }

# All text being written across Write (.content), Edit (.new_string), MultiEdit (.edits[]).
hook_written() {
  printf '%s' "$HOOK_INPUT" | jq -r '
    [ .tool_input.content, .tool_input.new_string, ( .tool_input.edits[]?.new_string ) ]
    | map(select(. != null)) | join("\n")'
}

# True when $1 is a real `git commit` invocation (not --help/-h).
hook_is_git_commit() {
  printf '%s' "${1:-}" | grep -qE '(^|[^[:alnum:]_])git[[:space:]]+(-[^[:space:]]+[[:space:]]+)*commit($|[[:space:]])' || return 1
  case "${1:-}" in *--help* | *' -h'*) return 1 ;; esac
}

# Emit a PreToolUse response.
hook_emit_context() { jq -cn --arg ctx "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",additionalContext:$ctx}}'; }
hook_emit_deny() { jq -cn --arg r "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$r}}'; }
