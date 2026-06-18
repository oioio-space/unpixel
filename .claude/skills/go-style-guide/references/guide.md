# Go Style Guide — Core Principles

Source: https://google.github.io/styleguide/go/guide

These are the overarching goals. The detailed, enforceable rules live in
`decisions.md` and `best-practices.md`. When two principles conflict, the one
higher in this list wins.

## 1. Clarity

The code's purpose and rationale are clear to the reader.

- **What is the code doing?** Make purpose transparent via descriptive names,
  targeted comments, strategic whitespace, and modular functions. Optimize for the
  reader, not the writer.
- **Why is the code doing it?** Explain non-obvious rationale (language nuances,
  business logic) with meaningful names and comments — not redundant restatements.

## 2. Simplicity

Code should be simple to use, read, and maintain.

- **Least mechanism.** Prefer plain language constructs (channels, slices, maps)
  → then the standard library → then internal libraries → and only last external
  dependencies. Don't introduce machinery (frameworks, reflection, codegen, deep
  abstraction) before it earns its place.
- Propagate values clearly and return useful error messages.

## 3. Concision

High signal-to-noise ratio.

- Minimize repetition, extraneous syntax, unclear names, and unnecessary abstraction.
- Lean on idioms readers already recognize. Concision is not golf — clarity still wins.

## 4. Maintainability

"Code is edited many more times than it is written."

- Design APIs that grow gracefully.
- Make assumptions explicit; minimize coupling.
- Provide comprehensive tests with clear diagnostics.

## 5. Consistency

Consistent code looks, feels, and behaves like similar code across the codebase.

- Keep uniformity within a package and a file.
- Consistency is a tie-breaker — it does NOT override the documented principles
  above. Don't propagate or worsen an existing local deviation.

## Formatting & naming rules from the guide

- **Formatting** — all source must match `gofmt` output (presubmit-enforced).
  Generated code should also be formatted.
- **MixedCaps** — multi-word names use `MaxLength` / `mixedCaps`, never underscores,
  even where another convention would use them.
- **Line length** — no fixed maximum. Don't split a line just to fit a width;
  refactor instead. Avoid breaking before an indentation change or splitting strings.
- **Naming** — avoid repetition given context; Go names run a bit shorter than other
  languages; don't duplicate a concept already present in the surrounding context.
- **Local consistency** — you may match an unstated local style, but never worsen an
  existing deviation, spread it wider, or introduce a bug to satisfy it.
