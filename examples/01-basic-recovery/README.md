# 01 · Basic recovery — read text hidden behind a mosaic

**What this shows:** the core UnPixel trick. When text is hidden with a *mosaic*
(the redaction is split into square blocks and each block is replaced by its average
colour), the pixels still carry information. UnPixel renders every candidate string,
re-applies the *same* pixelation, and keeps the candidate whose blocks match the
redaction — recovering the original text.

## The images in this folder

Each PNG is one variant this feature handles — a short secret redacted at block size
8 in the default font:

| image | hidden text |
|-------|-------------|
| `images/admin.png` | `admin` |
| `images/hello.png` | `hello` |
| `images/Go2.png`   | `Go2`   |
| `images/cat.png`   | `cat`   |

## For everyone — the CLI

The `unpixel` command does this out of the box. You give it the image and the
alphabet the secret could be spelled from (the *charset*); the narrowest alphabet
that fits keeps the search fast:

```console
$ unpixel --redaction mosaic --charset "admin xyz0" -b 8 images/admin.png
admin
```

- `--redaction mosaic` — tell it the redaction is a block mosaic (the default `auto`
  can misread a tiny synthetic image as blur; on real screenshots `auto` is fine).
- `--charset "admin xyz0"` — the candidate characters. Fewer characters = faster.
- `-b 8` — the block size in pixels (pass `0` or omit to auto-detect).

No `--font`? UnPixel sweeps its built-in fonts and keeps the best fit, so you don't
need to know the typeface.

## For developers — the code

The same recovery from Go is one call, [`unpixel.Recover`](https://pkg.go.dev/github.com/oioio-space/unpixel#Recover).
See [`main.go`](main.go); run it with `go run .`:

```go
res, err := unpixel.Recover(ctx, img,
    unpixel.WithCharset("admin xyz0"),
    unpixel.WithBlockSize(8),
)
fmt.Println(res.BestGuess) // "admin"
```

Running the program in this folder:

```console
$ go run .
admin.png  -> "admin"  (distance 0.0000)
hello.png  -> "hello"  (distance 0.0000)
Go2.png    -> "Go2"    (distance 0.0000)
cat.png    -> "cat"    (distance 0.0000)
```

`res.BestTotal` is the whole-image distance in `[0,1]` — `0.0000` means the recovered
text re-pixelates to *exactly* the redaction (a certain match). Import the `defaults`
package for its side effect (`import _ ".../defaults"`) to wire the standard
renderer, pixelator, metric and search strategy.

## How it works (advanced)

Recovery is generate-and-test guided search:

1. **Render** a candidate string with a font renderer.
2. **Re-pixelate** it with the same block-average operator, at each possible grid
   origin (the search absorbs sub-block padding this way).
3. **Score** the result against the redaction with an image metric (pixelmatch).
4. **Guide** a depth-first search by that score, extending the most promising prefix
   first and backtracking when a block stops matching.

Because the operator is applied *identically* to the candidate and the original, a
correct string reproduces the redaction's blocks with distance ≈ 0.

## Notes & limits

- Prefer the **narrowest charset** that can spell the secret — the search space is
  `charset^length`.
- A **coarser block** (larger `-b`) hides more information; see
  [`../02-block-sizes`](../02-block-sizes).
- High-entropy secrets at coarse blocks can become physically ambiguous (several
  strings produce the same blocks) — see the project's `docs/concepts/limits.md`.
