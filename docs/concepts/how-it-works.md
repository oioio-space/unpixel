# How it works

## The central principle: redaction is reversible

Pixelation and blur may appear to destroy information, but both are **deterministic
operations**. Pixelation replaces each block of pixels with that block's average colour;
blur combines each pixel with its neighbours according to a fixed formula. Applying the
same operation to the same input always yields the same output.

UnPixel does not attempt to sharpen or invert the image — that information is genuinely
lost. Instead, it proceeds **by hypothesis and verification**:

1. **Render** a candidate string in the target font.
2. **Re-apply the redaction** — pixelate or blur the candidate using the *same*
   parameters as the image under analysis.
3. **Compare** the result with the redacted region, pixel by pixel.
4. **Retain** the candidates that match and discard the remainder.

Because the redaction is deterministic, **only the true text reproduces the redacted
blocks exactly.** An incorrect candidate produces different block averages and is
rejected. This procedure is known as **generate-and-test**.

## Efficient search

Exhaustive enumeration is infeasible, as the candidate space is far too large. UnPixel
therefore constructs the solution one character at a time by means of a **guided
search**:

- It first determines the **grid offset** — the precise alignment of the pixelation
  blocks.
- It then extends the text character by character. After each addition it re-applies the
  redaction and scores the result, **pruning** any branch as soon as its output ceases to
  match the target.

In this manner, entire families of incorrect solutions are eliminated early, without
rendering the many strings beneath them.

## Tie-breaking by plausibility

Two distinct candidates may occasionally produce nearly identical blocks; small fonts
combined with large blocks carry little information per character. When the image score
cannot discriminate, UnPixel applies optional **plausibility priors**:

- a dictionary of valid words (English and French),
- common passwords,
- recognized secret formats (UUIDs, API tokens, Luhn-valid card numbers).

A candidate resembling natural language or a structured secret receives a modest bonus
and is ranked above arbitrary noise. These priors break ties only; they never override
unambiguous image evidence.

## The significance of a pure-Go implementation

The inner loop — render, re-apply, compare — executes an enormous number of times. The
original Bishop Fox tool rendered each candidate by capturing a screenshot from a
Chromium browser (milliseconds per candidate). UnPixel rasterizes text in process with
`golang.org/x/image` (microseconds per candidate) and distributes the search across all
available CPU cores with a deterministic merge — producing the same result irrespective
of timing. This is what renders the exhaustive search practical. The figures are given in
[comparison](../comparison.md) and [performance](../performance.md).

## Where the problem becomes difficult

Generate-and-test succeeds only when the rendered candidate matches the manner in which
the original text was drawn. The **font** is the critical ingredient: if it is incorrect,
every candidate's blocks are slightly displaced and none matches well. For this reason,
supplying the exact font is the most significant real-world factor. See
[fonts & calibration](fonts-and-calibration.md) and the candid
[limitations](limits.md).

## Further detail

- The exact faithful pipeline, including every crop and threshold:
  [architecture](../architecture.md).
- The available search and decoder strategies: [decoders](decoders.md).
