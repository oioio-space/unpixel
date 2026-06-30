# UnPixel sidecar protocol

## Purpose

UnPixel verifies candidate restored images — it does **not** run an image restorer.
An external restorer (a diffusion model, a Python sidecar, an LLM image-generation
service) proposes N candidate clean images for a redacted band.  UnPixel's
`VerifyImage` / `unpixel_verify_image` acts as the anti-hallucination gate: it
re-applies the forward operator (re-pixelate or re-blur) to each candidate and
compares the result to the observed redaction with the faithful pixel metric.
Only candidates whose re-pixelation is physically consistent with the observed
mosaic (`match=true`) pass the gate.

## Anti-hallucination loop

```
┌──────────────────────────────────────────────────────────────────┐
│ 1. Orchestrator calls unpixel_propose_hints(redacted_image)      │
│    → receives: block_size, char_count, bbox, charset_hint,       │
│                leaked_context                                     │
│                                                                   │
│ 2. Orchestrator calls external restorer with those hints         │
│    → restorer produces N candidate restored images               │
│                                                                   │
│ 3. For each candidate restored image:                            │
│    Orchestrator calls unpixel_verify_image(redacted, restored)   │
│    → receives: {distance, match}                                 │
│                                                                   │
│ 4. Keep candidates where match=true                              │
│    If multiple survive: ambiguity is unresolved by design        │
└──────────────────────────────────────────────────────────────────┘
```

UnPixel is a pure-Go library; it calls no external processes (`os/exec` is absent
from the codebase).  The orchestrator drives both sides.

## Illustrative JSON contract

### Restorer input (orchestrator → restorer sidecar)

```json
{
  "redaction_png_b64": "<base64-encoded PNG of the redacted band>",
  "block_size": 8,
  "char_count_estimate": 5,
  "charset_hint": "abcdefghijklmnopqrstuvwxyz ",
  "bbox": { "x": 0, "y": 0, "w": 120, "h": 48 }
}
```

### Restorer output (restorer sidecar → orchestrator)

```json
[
  { "restored_png_b64": "<base64 PNG, candidate 1>" },
  { "restored_png_b64": "<base64 PNG, candidate 2>" }
]
```

### UnPixel verify call (MCP tool)

```json
{
  "tool": "unpixel_verify_image",
  "arguments": {
    "redacted_png_b64": "<base64 PNG>",
    "restored_png_b64": "<base64 PNG, candidate 1>",
    "block_size": 8
  }
}
```

### UnPixel verify response

```json
{
  "distance": 0.0012,
  "match": true
}
```

UnPixel only consumes the verify side.  The restorer wire format is the
integrator's responsibility — UnPixel does not prescribe it.

## Honest limits

- **Mosaic ambiguity is not disambiguable.** Multiple distinct texts can produce
  an identical pixelated mosaic (block averages collapse many source images to
  the same output).  A `match=true` verdict means the candidate is *physically
  consistent* with the observed redaction — not that it is the unique correct
  answer.  Where genuine ambiguity exists, several candidates will all pass the
  gate; no physical check can break that tie.

- **Blur > mosaic.** Gaussian-blurred redactions are harder than mosaic redactions.
  The phase-search in `VerifyImage` is tuned for block-average mosaics; blur
  pipelines require an explicit blur `Pixelator` (via `WithPixelator`) and the
  metric distances are less sharp.

- **Phase-search is optimistic.** `VerifyImage` tries multiple grid phases and
  keeps the best distance.  A hallucination that happens to align well at one
  phase may score lower than expected; the `VerifyMatchThreshold` (0.10) is
  calibrated to leave a clear gap for typical mosaic block sizes ≥ 6 px.

- **The diffusion model is deferred.** This release ships the verification gate
  and the sidecar protocol — not a bundled image restorer.  Integrators must
  supply their own restoration model.

- **No `os/exec` in UnPixel.** The restorer is a separate process or MCP server
  that the orchestrator drives independently.  UnPixel never spawns subprocesses.
