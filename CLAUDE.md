# UnPixel — project guidance

State & roadmap: see `PROGRESS.md`. Tooling is managed by **mise** — run everything with
`mise run …` (lint, test, ci, fmt, scan:*, sbom, gh, …). Commits go through git hooks
(secrets → vulns → style) and Claude PreToolUse hooks (style/simplify/secret/vuln review,
modern-Go injection on `.go` edits).

## Research-first

Ground non-trivial / technical answers in CURRENT external sources before answering —
WebSearch/WebFetch for the state of the art, GitHub (`scripts/ghx.sh`) for prior art &
libraries, official docs (microsoft-docs MCP, claude-code-guide), `deep-research` for big
questions. **Then go beyond the existing**: critique it, look for improvements, and consider
out-of-the-box / novel approaches — don't just replicate prior art, improve on it. Prefer
sources over memory, compare alternatives, recommend the best (even if uncommon), cite. A
`UserPromptSubmit` hook (`.claude/hooks/research-grounding.sh`) reinforces this every prompt;
skip for trivial asks.

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
