# UnPixel examples

Runnable, self-contained examples — **one folder per feature**. Each folder has:

- a **`README.md`** explaining the feature for newcomers *and* advanced users, with a
  **CLI** example and an equivalent **Go code** example;
- a **`main.go`** program you can run with `go run .` from inside the folder;
- an **`images/`** directory whose PNGs are variants the feature handles.

Every program decodes the images shipped alongside it, so the examples double as a
living test of the documented behaviour.

## The examples

| # | folder | feature | CLI | code |
|---|--------|---------|-----|------|
| 01 | [`01-basic-recovery`](01-basic-recovery) | Read text behind a mosaic | `unpixel <img>` | `unpixel.Recover` |
| 02 | [`02-block-sizes`](02-block-sizes) | Recovery at coarse blocks (20–32 px) | `-b <n>` | `WithBlockSize` |
| 03 | [`03-narrow-charsets`](03-narrow-charsets) | PINs, hex tokens, structured secrets | `--charset-preset digits\|hex` | `WithCharset` / `CharsetDigits` |
| 04 | [`04-propose-and-verify`](04-propose-and-verify) | Confirm a proposed guess physically | *(library only)* | `unpixel.Verify` |
| 05 | [`05-perspective`](05-perspective) | Redactions photographed at an angle | *(library only)* | `mosaictext.DecodePerspective` |
| 06 | [`06-automation`](06-automation) | Machine-readable results + reliability gate | `--format json` / `--strict` | `Result` fields |

## Running them

```console
# The CLI (build it once):
$ go build -o unpixel ./cmd/unpixel
$ ./unpixel --redaction mosaic --charset "admin xyz0" -b 8 examples/01-basic-recovery/images/admin.png
admin

# A code example (from inside any example folder):
$ cd examples/01-basic-recovery && go run .
admin.png  -> "admin"  (distance 0.0000)
...
```

## A note on the CLI examples

The bundled images are **synthetic fixtures** with an exactly-known geometry, so the
CLI examples pass an explicit `--charset`, `-b`, and (where the render is large)
`--font-size`, and force `--redaction mosaic` (the `auto` detector can mistake a tiny
synthetic image for blur). On a **real screenshot** you can usually just run
`unpixel screenshot.png` and let auto-detection handle the block size, font, and
redaction type.

## Where recovery stops

These examples show what works. UnPixel is deliberate about its limits: high-entropy
secrets at coarse blocks can be information-theoretically ambiguous, and real
redactions in unbundled fonts need either the exact font or the
[propose-and-verify](04-propose-and-verify) path. The authoritative account is
[`docs/concepts/limits.md`](../docs/concepts/limits.md).
