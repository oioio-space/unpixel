# Pre-commit Go style review — checklist

You (Claude) are about to commit Go code. Before running `git commit`, review the
**staged diff** (`git diff --cached`) against EVERY item below — these are the
Google Go Style Guide items that linters cannot fully judge. For anything you can't
confirm, fix it or call it out. Then proceed only if the diff is clean.

## Principles (priority order, earlier wins)
- [ ] Clarity — purpose and rationale are obvious to a reader.
- [ ] Simplicity / least mechanism — no abstraction or dependency that isn't earned.
- [ ] Concision — no repetition, dead code, or noise.
- [ ] Maintainability — designed to be edited; assumptions explicit; tests present.
- [ ] Consistency — matches surrounding code (tie-breaker only).

## Naming
- [ ] MixedCaps, no underscores; initialisms keep case (`URL`, `ID`, `userID`).
- [ ] No `Get` prefix on getters; value-returning funcs read as nouns, actions as verbs.
- [ ] Names don't repeat package/receiver/type already in context (`http.Server`, not `http.HTTPServer`).
- [ ] Receiver names: 1–2 letters, consistent across the type.
- [ ] No `util`/`helper`/`common` packages; no names shadowing stdlib packages.
- [ ] Variable name length matches scope.

## Comments & docs
- [ ] Every exported symbol has a doc comment starting with its name, full sentence, ending with a period.
- [ ] Comments explain WHY (non-obvious rationale), not WHAT the code already says.
- [ ] Package comment present, directly above `package`, `// Package x …` form.

## GoDoc / pkg.go.dev (write docs that render, not just exist)
Exported API is read on pkg.go.dev — make it read well there, before each commit:
- [ ] Exported **struct fields**, interface methods, and `const`/`var` each carry their own doc comment (they render individually; don't rely on the type comment alone).
- [ ] Package doc gives a real **overview**: what it does, the approach in a sentence, and how to USE it — blank `//` lines between paragraphs; an indented code snippet for a usage hint renders as a code block.
- [ ] Doc comments state **contracts**: zero-value/default behavior, ownership (who closes a channel / frees a resource), nil handling, and which error a func returns when.
- [ ] Public packages have a runnable **`Example`** (in `pkg_test`) so pkg.go.dev shows usage; it must compile (omit `// Output:` if it shouldn't run under `go test`).
- [ ] GoDoc formatting is valid: headings (`# Heading`), lists, and `[Type]`/`[pkg.Symbol]` doc links where they help; no stale signatures in prose.
- [ ] Sanity-check with `go doc ./<pkg>` (and `go doc ./<pkg> Symbol`) — it should read as good standalone documentation.

## Errors
- [ ] Error strings lowercase, no trailing punctuation.
- [ ] Errors handled immediately; happy path un-indented; no `else` after a returning `if`.
- [ ] `%w` (at end) when callers need `errors.Is`/`errors.As`; `%v` at system boundaries.
- [ ] Added context isn't redundant with the wrapped error; no "it failed" non-annotations.
- [ ] No branching on `err.Error()` strings; use sentinels / typed errors.
- [ ] No in-band error values (`-1`, `""`, `nil`); return an extra `bool`/`error`.

## Language & structure
- [ ] Interfaces small and defined at the consumer; none added "just in case".
- [ ] Pointer vs value receivers correct & consistent; no copying types with a Mutex.
- [ ] No panic for ordinary failures; goroutines have explicit exit conditions.
- [ ] `context.Context` is the first param, never stored in a struct.
- [ ] Long argument lists factored (option struct / variadic options).
- [ ] `any` over `interface{}`; `%q` for quoted strings.

## Tests
- [ ] `got` before `want`; failure messages identify function + inputs.
- [ ] `t.Error` to keep going; `t.Fatal` only when continuing is meaningless.
- [ ] Field names in table-driven struct literals; no ad-hoc assertion helpers (use `cmp`).
- [ ] Never `t.Fatal` from a spawned goroutine.

See `.claude/skills/go-style-guide/references/` for the full itemized guides.
