# References

The research, prior art, libraries, and fonts UnPixel builds on.

## Prior art & original tool

- **Bishop Fox — unredacter.** The proof-of-concept that demonstrated mosaic
  pixelation of text is reversible, and the technique UnPixel ports.
  [Repository](https://github.com/bishopfox/unredacter) ·
  [Write-up: *Never use pixelation to redact text*](https://bishopfox.com/blog/unredacter-tool-never-pixelation).
- **Depix** (Sipke Mellema). Reference-matching ("De-Pix") recovery of pixelated text,
  the basis for UnPixel's `ref-match` decoder.
  [Repository](https://github.com/beurtschipper/Depix).
- **DepixHMM** (Jonas Schatz). An HMM-based depixelization following the Hill et al.
  approach; inspiration for UnPixel's HMM decoders.
  [Repository](https://github.com/JonasSchatz/DepixHMM).

## Research papers

- **Hill, Zhou, Saul, Shacham (2016) — "On the (In)effectiveness of Mosaicing and
  Blurring as Tools for Document Redaction."** *Proceedings on Privacy Enhancing
  Technologies (PoPETs)* 2016(4); PETS 2016. Learned-emission HMM + Viterbi recovery of
  redacted text, the re-mosaic-for-blur composite operator, and the offset-sensitivity
  results. Basis for UnPixel's `trained-hmm`, `window-hmm`, and `--remosaic`.
  [PoPETs page](https://petsymposium.org/popets/2016/popets-2016-0047.php) ·
  [PDF](https://hovav.net/ucsd/dist/redaction.pdf).
- **Kopec, Chou (1994) — "Document Image Decoding Using Markov Source Models."** *IEEE
  TPAMI* 16(6):602–617. The Document-Image-Decoding (DID) trellis / source-channel
  formulation behind UnPixel's `did` decoder.
  [DOI:10.1109/34.295910](https://doi.org/10.1109/34.295910).
- **Pan, Hu, Su, Yang (2014) — "Deblurring Text Images via L0-Regularized Intensity and
  Gradient Prior."** *CVPR 2014*. The non-blind text-deblurring prior behind UnPixel's
  `--l0-deblur`.
  [CVF open access](https://openaccess.thecvf.com/content_cvpr_2014/html/Pan_Deblurring_Text_Images_2014_CVPR_paper.html) ·
  [Project page](https://jspan.github.io/projects/text-deblurring/index.html).

## Libraries

- [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image) — pure-Go font
  rasterizer (`font/opentype`, `font.Drawer`) and `draw` for padding/compositing.
- [`github.com/orisano/pixelmatch`](https://github.com/orisano/pixelmatch) — faithful
  Go port of [mapbox/pixelmatch](https://github.com/mapbox/pixelmatch); the default
  image-distance metric.

## No-CGO GPU study (for the performance discussion)

- [gogpu/wgpu — pure-Go WebGPU (compute), zero-CGO](https://github.com/gogpu/wgpu) ·
  [gogpu/gogpu](https://github.com/gogpu/gogpu)
- [go-webgpu/goffi — pure-Go FFI for wgpu-native (purego)](https://github.com/go-webgpu/goffi)
- [purego — call C libraries without CGO](https://github.com/ebitengine/purego)
- [Go 1.26 release notes — `simd/archsimd` under `GOEXPERIMENT=simd`](https://go.dev/doc/go1.26)

See [performance.md](../performance.md) for how these were evaluated and why CPU stays
the default.

## Fonts

The default renderer embeds **Liberation Sans** (≈ Arial, SIL OFL 1.1). The zero-config
sweep also bundles Liberation Serif/Mono, Carlito (≈ Calibri), Source Code Pro, and
JetBrains Mono (all SIL OFL 1.1), and Caladea (≈ Cambria, Apache 2.0) — unmodified,
with attribution and license texts in [`fonts/`](../../fonts) (`NOTICE.md` +
`licenses/`).
