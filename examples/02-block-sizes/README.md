# 02 · Block sizes — recovery at coarse mosaics

**What this shows:** the mosaic *block size* is the single biggest factor in how much
information a redaction destroys. A bigger block averages a larger area, so it hides
more — but a large block is **not** automatically safe. As long as a few
information-rich blocks still span each character, UnPixel recovers the text. Here the
same word is redacted at blocks 20, 24 and 32 px and all recover exactly.

## The images in this folder

| image | block | hidden text |
|-------|-------|-------------|
| `images/go_block20.png` | 20 px | `go` |
| `images/go_block24.png` | 24 px | `go` |
| `images/go_block32.png` | 32 px | `go` |
| `images/cat_block20.png`| 20 px | `cat` |

These were rendered large (80–128 pt) so a short word still spans a few coarse blocks.

## For everyone — the CLI

Pass the block size with `-b`. On real screenshots you can omit it (`-b 0`) and
UnPixel auto-detects it and the font size:

```console
$ unpixel --redaction mosaic --charset "go abcdef" -b 20 --font-size 80 images/go_block20.png
go
```

## For developers — the code

[`unpixel.WithBlockSize`](https://pkg.go.dev/github.com/oioio-space/unpixel#WithBlockSize)
sets the block; `WithStyle` pins the font size so the forward model matches the
fixture. See [`main.go`](main.go) (`go run .`):

```go
res, _ := unpixel.Recover(ctx, img,
    unpixel.WithCharset("go abcdef"),
    unpixel.WithBlockSize(24),
    unpixel.WithStyle(unpixel.Style{FontSize: 96, PaddingTop: 8, PaddingLeft: 8}),
)
```

```console
$ go run .
go_block20.png   (block 20) -> "go"   (distance 0.0000)
go_block24.png   (block 24) -> "go"   (distance 0.0000)
go_block32.png   (block 32) -> "go"   (distance 0.0000)
cat_block20.png  (block 20) -> "cat"  (distance 0.0000)
```

## Advanced — where the wall really is

Block size alone doesn't defeat recovery; **block size relative to glyph size** does.
When a block grows to where each character spans only ~1 block column, the per-block
averages of many different strings collapse together and the redaction becomes
information-theoretically ambiguous. Two levers change the outcome:

- **Font/render size** — a larger rendering spreads each glyph over more blocks, so a
  "coarse" block can still be recoverable (as here at 128 px).
- **Multiple grid phases** of the same content are the only genuine super-resolution
  lever for block-average mosaics (see `mosaictext.DecodeMultiFrame`).

The project's `docs/concepts/limits.md` quantifies this boundary.
