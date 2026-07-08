# 05 · Perspective — redactions photographed at an angle

**What this shows:** a redaction isn't always a neat, front-facing rectangle. Photograph
a pixelated screen with a phone and the mosaic becomes a *tilted quadrilateral*.
`mosaictext.DecodePerspective` finds the redaction's four corners, un-warps them back
to a flat rectangle (a homography / perspective transform), and then runs the normal
recovery on the rectified image.

> This is a library API (`mosaictext.DecodePerspective`); there is no CLI sub-command.

## The images in this folder

| image | hidden text | tilt |
|-------|-------------|------|
| `images/go_tilted.png`    | `go`    | keystoned |
| `images/cat_tilted.png`   | `cat`   | keystoned |
| `images/hello_tilted.png` | `hello` | keystoned (longer word) |

## For developers — the code

Let the library detect the tilted region with `WithPerspectiveAutoQuad`, then give it
the same charset/size/block hints as a normal decode. See [`main.go`](main.go):

```go
res, _ := mosaictext.DecodePerspective(ctx, img,
    mosaictext.WithPerspectiveAutoQuad(0),      // find the tilted redaction automatically
    mosaictext.WithPerspectiveCharset("go abcd"),
    mosaictext.WithPerspectiveFontSize(32),
    mosaictext.WithPerspectiveBlockSize(8),
)
fmt.Println(res.Text) // "go"
```

```console
$ go run .
go_tilted.png    -> "go"   (distance 0.0132)
cat_tilted.png   -> "cat"  (distance 0.0082)
hello_tilted.png -> "hebo" (distance 0.0242)
```

## Advanced — the honest limit of auto-detection

`hello_tilted.png` comes back as `hebo`, not `hello` — a deliberately shown *near
miss*. Auto-quad recovers the corners to within a few pixels; that small error is
harmless for a 2–3 letter word but **compounds along a longer string** after the
un-warp, nudging some glyphs into their neighbours. Two ways to fix it:

- Supply the **exact corners** with `WithPerspectiveQuad(...)` (removes the
  detection error — the longer word then recovers exactly), and the rectangle size
  with `WithPerspectiveRectSize`.
- Keep the words short, or verify the auto result with
  [propose-and-verify](../04-propose-and-verify).

The distance column tells you which is which: `0.0082` (exact) vs `0.0242` (a
close-but-wrong rectification).
