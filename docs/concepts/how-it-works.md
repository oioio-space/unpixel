# How it works

## The key idea: redaction is reversible

Pixelation and blur feel like they destroy information, but both are **deterministic
recipes**. Pixelation replaces every block of pixels with that block's average color.
Blur mixes each pixel with its neighbors by a fixed formula. Run the same recipe on
the same input and you always get the same output.

UnPixel doesn't try to "sharpen" or "un-blur" the image — that information really is
gone. Instead it works **backwards by guessing and checking**:

1. **Render** a candidate piece of text in the target font.
2. **Re-apply the redaction** — pixelate or blur it with the *same* settings as the
   image you're attacking.
3. **Compare** the result to the redacted region, pixel by pixel.
4. **Keep** the guesses that match and throw away the rest.

Because the redaction is deterministic, **only the true text reproduces the redacted
blocks exactly.** A wrong guess produces different block averages and gets rejected.
This is called **generate-and-test**.

## Searching efficiently

You can't try every possible string — there are far too many. UnPixel builds the
answer up one character at a time using a **guided search**:

- First it finds the **grid offset** — exactly where the pixelation blocks line up.
- Then it extends the text character by character. After each new character it
  re-pixelates and scores the result, and **prunes** any branch the moment its output
  stops matching the target.

This way whole families of wrong answers are eliminated early, without ever rendering
the millions of strings underneath them.

## Breaking ties with plausibility

Sometimes two different guesses produce nearly identical blocks (small fonts and large
blocks carry little information per character). When the image score can't decide,
UnPixel uses optional **plausibility priors** to break the tie:

- a dictionary of real words (English and French),
- common passwords,
- recognized secret formats (UUIDs, API tokens, Luhn-valid card numbers).

A guess that looks like real language or a structured secret gets a small bonus, so it
ranks above random noise. These priors only break ties — they never override clear
image evidence.

## Why pure Go matters here

The inner loop — render → re-pixelate → compare — runs an enormous number of times.
The original Bishop Fox tool rendered each candidate by screenshotting a Chromium
browser (milliseconds per candidate). UnPixel rasterizes text in-process with
`golang.org/x/image` (microseconds per candidate) and fans the search out across all
your CPU cores with a deterministic merge — the same answer regardless of timing. That
makes the brute-force search practical. See [comparison](../comparison.md) and
[performance](../performance.md) for the numbers.

## Where it gets hard

Generate-and-test only works if your rendered candidate matches how the original text
was drawn. The **font** is the critical ingredient — if it's wrong, every candidate's
blocks are slightly off and nothing matches well. That's why supplying the exact font
is the biggest real-world lever. See [fonts & calibration](fonts-and-calibration.md)
and the honest [limits](limits.md).

## Going deeper

- The exact faithful pipeline (every crop and threshold): [architecture](../architecture.md).
- The different search/decoder strategies: [decoders](decoders.md).
