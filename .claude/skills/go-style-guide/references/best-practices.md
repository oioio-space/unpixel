# Go Best Practices — Itemized

Source: https://google.github.io/styleguide/go/best-practices

Patterns that go beyond the normative decisions. ✅ = do, ❌ = avoid.

## Naming

- **Avoid repetition** — omit from a function/method name what the call site already
  conveys: the package, the receiver type, input/output types (when no collision).
  ✅ `users.New()`  ❌ `users.NewUser()`; ✅ `(c *Config) Load()`  ❌ `(c *Config) LoadConfig()`.
- **Disambiguating similar names** — add the distinguishing detail when two functions
  would otherwise collide in meaning.
- **Noun- vs verb-like** — value-returning functions read as nouns (`Len`, `Name`);
  action functions read as verbs (`Parse`, `WriteTo`). No `Get` prefix.
- **Type-differentiated functions** — when variants differ only by type, suffix the
  type (`ParseInt`, `ParseFloat`); omit it for the primary version.

## Test Double & Helper Packages

- Name a test-double package by appending `test` to the production package
  (`creditcard` → `creditcardtest`).
- One double → simple name (`Stub`); name by behavior when several exist
  (`AlwaysFails`); use explicit `StubService` when several types need doubles.
- Prefix double variables to clarify their role at the call site (`spyClock`).

## Shadowing

- **Stomping** (`x = ...`, same scope) is fine when the old value is dead.
- **Shadowing** (`x := ...` in a nested scope) is dangerous — code after the block
  sees the OUTER variable. Watch `err :=` inside `if`/`for`.
- Don't use variable names that shadow standard-library packages (`url`, `path`,
  `time`). Pick package names that won't force import renames.

## Util / Package Size

- ❌ generic package names `util`, `helper`, `common`, `misc`. Name by what the
  package provides so call sites read well.
- Keep tightly-coupled implementations in one package (shared unexported access);
  combine packages users must always import together.
- Avoid both multi-thousand-line files and a swarm of tiny files; group related code
  by file. Use `doc.go` for long package docs.

## Imports

- Proto imports: `pb` suffix for `go_proto_library`, `grpc` suffix for grpc; prefer
  whole words over `xpb`.
- Follow the import grouping from `decisions.md`.

## Error Handling

### Structure
- Give errors structure so callers interrogate them programmatically.
- **Sentinel errors** — package-level `var ErrFoo = errors.New("foo")` for simple
  discrimination; compare with `errors.Is`, never `==` on a wrapped error.
- **String-based detection** — ❌ never branch on `err.Error()` substring matches.
- **Structured info** — expose detail via custom error types + `errors.As`, or gRPC
  status codes.

### Adding information
- Don't repeat what the wrapped error already says; add context from THIS function.
- Skip annotations that only say "it failed" without new information.
- **`%v` vs `%w`** — `%v` for a plain annotation that hides internals (system
  boundary); `%w` to preserve the chain for `errors.Is`/`errors.As`.
- Put `%w` at the END of the message (mirrors the chain); put it at the START only
  when wrapping a sentinel for prominence.

### Logging errors
- Either return the error OR log it — not both; let the top-most caller log once.
- Be careful with PII; use the ERROR level sparingly (it's expensive and alerts);
  ERROR-level messages should be actionable.
- Guard expensive log arguments behind verbosity checks.

### Initialization, checks & panics
- Propagate init errors up to `main` with actionable messages; prefer `log.Exit`
  over `log.Fatal` for init failures (`init` runs before flag parsing — use `panic`
  there if you must abort).
- Libraries return errors rather than aborting the program.
- `log.Fatal` only for unrecoverable invariant violations.
- ❌ don't `recover` just to keep a buggy program alive — fix the bug. Panic only on
  API misuse, genuinely unreachable code, or panics fully contained within a package.

## Documentation

- **Parameters/config** — document only error-prone or non-obvious params; don't
  restate the name/type; consider the audience (maintainer, newcomer, user).
- **Contexts** — cancellation interrupting the call is implied (don't restate);
  DO document non-`ctx.Err()` returns, other interruption mechanisms, and special
  deadline/value expectations.
- **Concurrency** — read-only safety is assumed; explicitly document when mutation is
  NOT concurrency-safe, when the API provides synchronization, and concurrency
  expectations of user-implemented interfaces.
- **Cleanup** — document required cleanup, which method releases resources, and give a
  runnable example when non-obvious.
- **Errors** — document significant sentinel/typed errors returned, whether the error
  type is pointer or value, and pointer-receiver implications for `errors.Is`.
- **Godoc formatting** — blank line separates paragraphs; indent code by two spaces
  for verbatim; a capitalized, unpunctuated line becomes a header.
- **Signal boosting** — comment non-obvious checks (e.g. an intentional `if err == nil`).

## Variable Declarations

- **Initialization** — `:=` when initializing with a non-zero value; `var` for the
  zero value.
- **Zero value** — declare with the zero value when used later, for unmarshal targets,
  and via `new(T)` for zero pointers. Use value types for fields that must not be
  copied (forces pointer receiver). Declare a pointer if you'll return a composite.
- **Composite literals** — use when elements are known; prefer zero values over empty
  composites (`var m map[K]V` vs `map[K]V{}` when not yet writing).
- **Maps** — initialize a map before writing; reading a nil map is fine.
- **Size hints** — preallocate capacity only with a known final size (e.g. map→slice)
  or measured benefit; most code should let the runtime grow; don't over-allocate.
- **Channel direction** — specify `<-chan`/`chan<-` direction in signatures to convey
  ownership and let the compiler catch misuse.

## Function Argument Lists

- Keep argument lists short. Split an over-configurable function into simpler ones,
  or use an **option struct** / **variadic options**.
- **Option struct** — self-documenting field names, omit defaults, shareable, grows
  without breaking call sites; NEVER put a `context` in it. Best when many callers
  configure frequently.
- **Variadic options** — zero call-site cost when unused; options are shareable values;
  accept parameters (not mere presence); booleans for on/off; enums for choices;
  later options override earlier; unexported param type to prevent third-party options.
  They cost more code — justify their use.

## Complex CLIs

- Use `cobra`/`subcommands`; in cobra use `cmd.Context()` not `context.Background()`.
- Don't force a package per subcommand. Treat the CLI as just another client of your
  library (library-as-client).

## Tests

- **Leave testing to test functions** — test helpers do setup/cleanup; assertion
  helpers are NOT idiomatic — report pass/fail in the `Test` function itself. Inline
  simple validation even if repetitive; prefer table-driven tests with inline checks.
  Shared validation returns values (no `testing.T`); use `cmp.Transformer` for reusable
  comparisons.
- **Extensible validation / acceptance tests** — ship a `…test` package with a blackbox
  exercise function that validates user implementations against the contract; document
  broken invariants; `t.Fatal` only for setup failures.
- **Use real transports** — connect a production client to a test-double server; don't
  hand-roll clients.
- **`t.Error` vs `t.Fatal`** — keep going by default with `t.Error`; `t.Fatal` for
  setup failure, before a table loop, or inside a `t.Run` subtest.
- **Helpers** — call `t.Helper()`; `t.Fatal` from a helper is fine (same goroutine);
  register cleanup with `t.Cleanup`; drop the `t` param if the helper can't fail.
- **Never `t.Fatal` from a spawned goroutine** — use `t.Error` + `return`; pass
  `testing.T` to the helper, not to goroutines it spawns.
- **Struct literals** — specify field names in table-driven test cases.
