# 06 · Automation — machine-readable results & a reliability gate

**What this shows:** how to *use* a recovery from a script or pipeline. Beyond the best
guess, UnPixel reports a **confidence** in `[0,1]`, a ranked list of **alternatives**,
and a whole-image **distance** — enough to decide, automatically, whether to trust the
answer. It also exposes exit codes so CI can tell "recovered" from "unrecoverable".

## The images in this folder

| image | hidden text |
|-------|-------------|
| `images/admin.png` | `admin` |
| `images/hello.png` | `hello` |

## For everyone — the CLI

`--format json` emits a structured result; `--min-confidence` refuses a low-confidence
guess; `--strict` turns "unreliable" into exit code **2** (distinct from a hard error's
exit **1** and a clean decode's exit **0**):

```console
$ unpixel --redaction mosaic --charset "admin xyz0" -b 8 --format json images/admin.png
{
  "best_guess": "admin",
  "font": "Liberation Sans",
  "top": [ { "guess": "admin", "score": 0 }, ... ]
}

# In CI: fail the step if the recovery is untrustworthy
$ unpixel --redaction mosaic --charset "..." -b 8 --strict image.png || echo "unrecoverable (exit $?)"
```

## For developers — the code

The [`Result`](https://pkg.go.dev/github.com/oioio-space/unpixel#Result) struct carries
`BestGuess`, `Confidence`, `BestTotal` (distance) and `TopN` (ranked alternatives). See
[`main.go`](main.go), which prints them and exits `2` when confidence is below a bar —
the library equivalent of `--strict`:

```go
res, _ := unpixel.Recover(ctx, img, unpixel.WithCharset("admin xyz0"), unpixel.WithBlockSize(8))
fmt.Printf("best=%q confidence=%.2f distance=%.4f\n", res.BestGuess, res.Confidence, res.BestTotal)
if res.Confidence < 0.5 {
    os.Exit(2) // don't trust it
}
```

```console
$ go run .
admin.png: best="admin" confidence=1.00 distance=0.0000
    #1 "admin"  score=0.0000
    ...
hello.png: best="hello" confidence=1.00 distance=0.0000
    ...
```

## Advanced

- **`Confidence`** = `1 − TopN[0].Score`; **`Ambiguity`** = the gap to the second-best
  candidate. A high confidence *and* a large ambiguity gap means a clear winner; a high
  confidence with near-zero ambiguity means several strings fit equally (a physical tie
  — see [propose-and-verify](../04-propose-and-verify)).
- **`BestTotal`** (whole-image distance) is comparable across runs, so it's the right
  signal when choosing between candidate fonts or configs; **`BestScore`** is a
  per-character marginal score that a correct prefix can drive to ~0.
- Wire these into a pipeline: decode → check `Confidence`/`BestTotal` → accept, or fall
  back to [propose-and-verify](../04-propose-and-verify) with human/LLM candidates.
