# UnPixel — project guidance

State & roadmap: see `PROGRESS.md`. Tooling is managed by **mise** — run everything with
`mise run …` (lint, test, ci, fmt, scan:*, sbom, gh, …). Commits go through git hooks
(secrets → vulns → style) and Claude PreToolUse hooks (style/simplify/secret/vuln review,
modern-Go injection on `.go` edits).

## Sub-agent routing (token-economical, no quality loss)

Each agent in `.claude/agents/` pins its own tier via frontmatter (`model`, `effort`, `skills`,
`description`-driven auto-delegation). This table is only the cross-agent rule of thumb — Claude
Code has no frontmatter field for a global policy, and the one global lever
(`CLAUDE_CODE_SUBAGENT_MODEL`) is intentionally NOT set because it would override every per-agent
`model`.

| Task | Agent | Model / effort |
|------|-------|----------------|
| Run checks (lint/test/ci/scan), trivial format fixes | `quality-runner` | Haiku / low |
| Docs, PROGRESS.md, commit messages | `scribe` | Haiku / low |
| Code/file search, fan-out, "where is X" | `explorer` (or built-in `Explore`) | Haiku / low |
| Implement Go code & tests, refactors, the port | `go-dev` | Sonnet / medium |
| Review a Go diff | `go-reviewer` | Sonnet / medium |
| Design the unredacter algorithm / architecture | `algo-architect` | Opus / high |
| Deep security / vuln audit | `security-auditor` | Opus / high |

Rule of thumb: mechanical → Haiku; writing/reviewing Go → Sonnet; novel algorithm design or
security judgement → Opus. Never use Opus for what a cheaper tier handles at equal quality.
Run independent sub-tasks in parallel.
