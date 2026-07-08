# 04 · Propose and verify — confirm a guess physically

**What this shows:** UnPixel's second mode. Instead of *searching* for the text, you
*propose* one or more candidate strings — from a person, an OSINT wordlist, or a
language model — and UnPixel **verifies** them: it renders each candidate, applies the
same pixelation, and measures how closely it reproduces the redaction. The truth
re-pixelates to a near-zero distance (a confirmed match); a wrong guess does not.

This is the reliable path for real screenshots (where blind search is
information-starved) and an **anti-hallucination gate** for any external guesser: a
faithful answer maps back to the observed mosaic; a hallucination doesn't.

> There is **no CLI sub-command** for verification — it is a library API. The natural
> workflow is: propose candidates in your own code (or via an LLM), then call
> `unpixel.Verify`. The CLI's job is the *search* side (examples 01–03).

## The images in this folder

| image | hidden text | candidates proposed |
|-------|-------------|---------------------|
| `images/admin.png` | `admin` | `admin`, `adman`, `user0` |
| `images/hello.png` | `hello` | `hello`, `hallo`, `world` |

## For developers — the code

[`unpixel.Verify`](https://pkg.go.dev/github.com/oioio-space/unpixel#Verify) scores
each candidate; [`WithVerifyThreshold`](https://pkg.go.dev/github.com/oioio-space/unpixel#WithVerifyThreshold)
sets how close a fit must be to count as a `Match`. See [`main.go`](main.go):

```go
verdicts, _ := unpixel.Verify(ctx, img,
    []string{"admin", "adman", "user0"},
    unpixel.WithBlockSize(8),
    unpixel.WithVerifyThreshold(0.05),
)
for _, v := range verdicts {
    fmt.Printf("%q  distance %.4f  match=%v\n", v.Text, v.Distance, v.Match)
}
```

```console
$ go run .
admin.png
  "admin"  distance 0.0000  ✓ confirmed
  "adman"  distance 0.1209  ·
  "user0"  distance 0.3766  ·
hello.png
  "hello"  distance 0.0000  ✓ confirmed
  "hallo"  distance 0.0635  ·
  "world"  distance 0.4416  ·
```

`VerifyBytes` / `VerifyReader` / `VerifyFile` are the same for in-memory, stream, or
file inputs.

## Advanced

- **`Distance`** is the whole-image pixelmatch distance in `[0,1]`; **`Match`** is
  `Distance < threshold`. The raw distance is always returned, so you can rank
  candidates yourself even when none passes the bar.
- Some substitutions are **physically identical** at coarse blocks (e.g. `0`↔`O`,
  `W`↔`N`) — several candidates tie near zero and only a *semantic* prior (a language
  model, a dictionary) can separate the real one. That is exactly why propose-verify
  pairs a physical check with an external proposer.
- This is the backbone of the "LLM proposes, physics verifies" loop UnPixel exists to
  demonstrate: pixelation is reversible whenever the content is *guessable*.
