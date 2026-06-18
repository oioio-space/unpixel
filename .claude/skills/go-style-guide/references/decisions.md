# Go Style Decisions — Itemized

Source: https://google.github.io/styleguide/go/decisions

One heading = one decision item. ✅ = do, ❌ = avoid.

## Naming

- **Underscores** — names contain no underscores. Exceptions: generated-code
  packages, `_test.go` identifiers (e.g. `Test_xxx`), low-level OS interop.
- **Package names** — short, lowercase, single unbroken word; no underscores or
  MixedCaps. Avoid names that shadow common local variables (e.g. don't name a
  package `count`).
- **Receiver names** — 1–2 letters, an abbreviation of the type, used consistently
  across all methods of that type. Never `self`, `this`, or underscores.
- **Constant names** — `MixedCaps`, like any other name. ❌ `MAX_LEN`, ❌ `kMaxLen`.
- **Initialisms** — keep consistent case: `URL`, `ID`, `HTTP`, `userID`, `urlPony`,
  `ServeHTTP`. ❌ `Url`, ❌ `userId`, ❌ `HTTPSURL` ambiguity → `HTTPSURL` stays caps.
- **Getters** — no `Get`/`get` prefix. ✅ `user.Name()`  ❌ `user.GetName()`. Keep
  `Get` only when the operation genuinely fetches (e.g. an HTTP GET).
- **Variable names** — length proportional to scope, inversely proportional to use
  frequency. Tiny scope → short name; package-level/exported → descriptive.
- **Single-letter names** — OK for receivers, loop indices (`i`, `j`), and very
  familiar type abbreviations (`r io.Reader`). Not for wider scopes.
- **Repetition** — avoid redundancy: `package`+symbol (`http.HTTPServer`→`http.Server`),
  variable type in name (`var numUsers int`→`var users int` when clear),
  context already implied.

## Commentary

- **Comment line length** — wrap long comments around 80–100 cols; no hard rule.
- **Doc comments** — every exported name gets a doc comment; also non-obvious
  unexported decls. Start with the name being declared: `// Foo does …`.
- **Comment sentences** — full sentences are capitalized and end with a period.
  Short fragments (e.g. inline) need not be.
- **Examples** — provide runnable `Example` functions in `_test.go`; they surface in
  Godoc.
- **Named result parameters** — name results when there are multiple of the same
  type, or when names clarify what the caller receives. Don't name them just to
  enable naked returns.
- **Package comments** — directly above `package` clause, no blank line between.
  Form: `// Package foo …`.

## Imports

- **Import renaming** — rename only to resolve a collision, clarify an
  uninformative name, or strip underscores from generated packages.
- **Import grouping** — group and blank-line-separate: (1) stdlib, (2) other
  imports, (3) protobuf, (4) side-effect (`_`) imports. (`goimports`/`gci` enforce.)
- **Import blank (`import _`)** — only in `main` or tests; never in a library.
- **Import dot (`import .`)** — never. It hides where symbols come from.

## Errors

- **Returning errors** — `error` is the final return value; non-nil signals failure.
- **Error strings** — lowercase, no trailing punctuation (they get embedded):
  ✅ `"something failed"`  ❌ `"Something failed."`. Exception: leading proper
  noun / exported name.
- **Handle errors** — handle immediately: return, or `log.Fatal`/`panic` only in
  truly exceptional cases. Never silently discard (`_ = err`) without reason.
- **In-band errors** — don't signal failure with sentinel values like `-1`/`""`/`nil`;
  return an extra `bool` or `error` (`value, ok :=`).
- **Indent error flow** — handle the error in the `if` block and keep the happy path
  at minimal indentation. ❌ `else` after an `if` that returns.

## Language

- **Literal formatting** — use composite literals; name fields for types from other
  packages.
- **Matching braces** — closing brace aligns with the line that opens the construct.
- **Cuddled braces** — allowed only when indentation matches and inner values are
  literals/proto builders.
- **Repeated type names** — omit the element type in slice/map literals when obvious:
  `[]T{{...}, {...}}`.
- **Zero-value fields** — omit zero-valued struct fields when it improves clarity.
- **Nil slices** — prefer `var s []T` (nil) over `[]T{}`; treat nil and empty alike.
- **Indentation confusion** — avoid wraps that make continuation align with a nested
  block.
- **Function formatting** — keep the signature on one line; introduce locals to
  shorten long argument lists rather than wrapping.
- **Conditionals and loops** — don't line-break `if`/`for` headers; hoist boolean
  operands into named variables.
- **Copying** — don't copy a value whose type has a `Mutex`, or that aliases via
  slices/maps/pointers. Mind `*T` vs `T` method sets.
- **Don't panic** — use `error` returns for ordinary failures; reserve panic for
  impossible conditions.
- **Must functions** — `MustXxx` helpers may panic on failure; use only at init /
  package scope / tests.
- **Goroutine lifetimes** — make exit conditions explicit (context, `WaitGroup`,
  closing a channel). Don't leak goroutines.
- **Interfaces** — define interfaces where they're consumed, not where implemented;
  keep them small; don't add an interface "just in case".
- **Generics** — use for genuine need; avoid premature polymorphism / building a DSL.
- **Pass values** — don't pass a pointer to a small fixed-size value just to "save a
  copy"; use pointers for large structs or proto messages.
- **Receiver type** — pointer receiver for mutation, non-copyable types, or large
  structs; value receiver otherwise. Be consistent within a type.
- **Switch and break** — omit redundant `break`; use a comment for an intentionally
  empty case.
- **Synchronous functions** — prefer synchronous APIs; let the caller add concurrency.
- **Type aliases** — `type T1 T2` defines a new type; reserve `type T1 = T2` aliases
  for migrations.
- **Use %q** — `%q` for quoted strings rather than manual quoting.
- **Use any** — prefer `any` over `interface{}` in new code.

## Common Libraries

- **Flags** — `snake_case` flag names, `camelCase` Go vars; define flags only in
  `package main`.
- **Logging** — use the standard leveled logger; `log.Fatal` only for abnormal exit.
- **Contexts** — `context.Context` is the first parameter (`ctx`); never store it in
  a struct; never define a custom context type.
- **crypto/rand** — use `crypto/rand` for keys/tokens, never `math/rand`.

## Useful Test Failures

- **Assertion libraries** — don't build assert helpers; use `cmp` + `fmt`/`t.Errorf`.
- **Identify the function** — failure messages include the function name and inputs.
- **Got before want** — print `got` then `want`: `got %v, want %v`.
- **Full structure comparisons** — compare whole structures (`cmp.Diff`) over
  field-by-field checks.
- **Compare stable results** — assert semantic equivalence, not brittle exact strings
  from other packages.
- **Keep going** — `t.Error` to report and continue; `t.Fatal` only when continuing is
  meaningless.
- **Equality comparison and diffs** — `cmp.Equal` / `cmp.Diff` for complex values.
- **Level of detail** — give enough context in the message to diagnose without the
  debugger.
