# UnPixel — project guidance

State & roadmap: see `PROGRESS.md`. Tooling is managed by **mise** — run everything with
`mise run …` (lint, test, ci, fmt, scan:*, sbom, gh, …). Commits go through git hooks
(secrets → vulns → style) and Claude PreToolUse hooks (style/simplify/secret/vuln review,
modern-Go injection on `.go` edits).

## Commands (all via mise; `CGO_ENABLED=0` pinned)

```bash
mise run setup           # bootstrap toolchain (go 1.26, golangci-lint, gotestsum, …)
mise run test            # unit tests        | mise run test:watch  (TDD)
mise run lint            # golangci-lint v2  | mise run fmt
mise run cover:check     # coverage gate (COVER_MIN=85)
mise run ci              # full gate = what CI runs (lint+test+cgo:check+scans)
mise run bench:baseline  # then change code, then: mise run bench:compare (benchstat)
mise run bench:panel     # recovery quality+speed panel over fixtures, diffed vs baseline
mise run bench:panel:record  # promote panel → baseline + append a version row to history
mise run scan:code       # gosec + govulncheck | scan:secrets | scan:sbom (grype)
mise run clean           # remove regenerable artifacts
```

## Architecture

Module `github.com/oioio-space/unpixel` — pure-Go port of `bishopfox/unredacter`
(generate-and-test depixelation: render → re-pixelate → image-distance → guided search).

- `unpixel.go` — root API: `Engine`, `Config`, `Result`, `Eval`, `Offset` + pluggable
  interfaces `Renderer`/`Pixelator`/`Metric`/`Strategy` + library-agnostic progress API.
- `internal/imutil` · `internal/pixelate` · `internal/metric` (pixelmatch default) ·
  `internal/render` (x/image + embedded Liberation Sans) · `internal/search` (offset
  discovery + GuidedDFS).  `defaults/` wires the standard components.  `cmd/unpixel` — CLI.
- Design & faithful-algorithm spec: `docs/DESIGN.md`.  State/roadmap: `PROGRESS.md`.

## ⛔ Absolute rule: NO CGO

This project is **pure Go — CGO is forbidden, no exceptions.** Never add `import "C"`, a
cgo-requiring dependency, or anything that needs a C toolchain. `CGO_ENABLED=0` is pinned in
`mise.toml [env]` (so every build/test is cgo-free) and enforced by the `cgo:check` gate
(`mise run cgo:check`, part of `ci` and the pre-commit gate). Pick pure-Go libraries only
(e.g. `golang.org/x/image`, not bindings). If a task seems to need CGO, find a pure-Go path or
stop and raise it — do not relax the rule.

## ⚡ Absolute rule: benchmark the hot path & prove perf changes

The per-candidate **render → re-pixelate → image-distance → search** core is a hot loop run over
a large space. It is an **absolute rule** that these hot-path packages (`internal/render`,
`internal/search`, `internal/pixelate`, `internal/metric`, `internal/imutil`) carry `Benchmark…`
tests, and that **any perf-affecting change is proven with benchstat** — never optimize by feel.
Workflow (go-benchmark skill): `mise run bench:baseline` → change → `mise run bench:compare`
(`-count` ≥ 10, `-benchmem`); keep the change only on a statistically significant gain with no
alloc/throughput regression. A `PreToolUse` hook (`.claude/hooks/benchmark-context.sh`) injects
benchmark guidance when you write a `func Benchmark…` and nudges when you edit a hot-path package.

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

### Skill → agent routing (cheapest competent tier)

Every skill is owned by the cheapest agent that runs it at full quality. Preload (frontmatter
`skills:`) only where no subagent-visible hook already injects the skill — otherwise the hook
would double the cost. Subagents do NOT receive the `UserPromptSubmit` hook (main-loop only),
but PreToolUse hooks DO fire inside subagents.

| Skill | Owner agent (tier) | Wiring |
|-------|--------------------|--------|
| `go-style-guide`, `use-modern-go` | `go-dev` / `go-reviewer` (Sonnet) | preloaded + `modern-go-context` hook |
| `go-benchmark` | `go-dev` (Sonnet) | hook-driven (`benchmark-context` fires in subagents) — not preloaded |
| `readme-author` | `scribe` (Haiku) | preloaded (no hook covers it) |
| `repo-janitor` | `quality-runner` (Haiku) | preloaded |
| `secret-guard`, `vuln-guard` | `security-auditor` (Opus) | preloaded |
| `research-grounding` | `algo-architect` (Opus) | preloaded (UserPromptSubmit hook skips subagents) |
| `helper-ergonomics` | `go-reviewer` (Sonnet) review, `go-dev` (Sonnet) implement | hook-driven (`commit-ergonomics-review`) — not preloaded |

### Review-hook → agent routing

The AI-review PreToolUse hooks (fire on `git commit`) name the cost-appropriate agent to
delegate to for a **substantial** diff; a trivial diff is cleared inline (delegating it would
cost more than it saves):

| Hook | Delegate to (tier) |
|------|--------------------|
| `commit-style-review` | `go-reviewer` (Sonnet) |
| `commit-cleanup-review` | `quality-runner` (Haiku) |
| `commit-secret-review` | `security-auditor` (Opus, only if ambiguous) |
| `commit-vuln-review` | `security-auditor` (Opus) |
| `commit-ergonomics-review` | `go-reviewer` (Sonnet/medium) review → `go-dev` implement |
| `commit-docs-review` | `scribe` (Haiku) README/PROGRESS → `quality-runner` (Haiku) runs `bench:panel:record` |

`commit-docs-review` fires when substantive library/CLI Go is staged but the human-facing
record isn't: it nudges syncing README.md + PROGRESS.md to the step's evolution and running
`mise run bench:panel:record` so decode quality+speed are tracked version-over-version in
`benchmarks/quality-history.md` (each row diffed against the previous). The recovery panel
itself lives in `panel_test.go` behind the `panel` build tag (out of the default test path).

Deterministic gates (`.githooks/*`, `cgo:check`) and pure context-injection hooks
(`modern-go-context`, `benchmark-context`, `research-grounding`) do no AI work → no routing.
