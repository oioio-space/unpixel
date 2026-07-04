package unpixel

import (
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/oioio-space/unpixel/internal/forensics"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// TestRecover_autoBlurSafeFallback verifies that a near-uniform image (zero
// variance) yields DetectBlur/DetectColorspace confidence well below 0.5, so
// applyAutoFingerprint must NOT install any pixelator — leaving cfg.Pixelator
// nil and preserving the byte-identical default path.
func TestRecover_autoBlurSafeFallback(t *testing.T) {
	// A blank image mosaiced with block=8 is uniform — no structure for the
	// detectors to work on, so confidence will be near zero.
	blank := image.NewRGBA(image.Rect(0, 0, 32, 16))
	img := pixelate.NewBlockAverage(8).Pixelate(blank, 0, 0)

	cfg := Config{BlockSize: 8}
	WithAutoBlur()(&cfg)
	WithAutoColorspace()(&cfg)
	applyAutoFingerprint(&cfg, imutil.ToRGBA(img))

	if cfg.Pixelator != nil {
		t.Errorf("Pixelator = %T, want nil (safe fallback on low-confidence input)", cfg.Pixelator)
	}
}

// TestRecover_autoFingerprintInstallsLinear verifies that a high-variance
// linear-light mosaic causes applyAutoFingerprint to install the LINEAR
// *pixelate.BlockAverage variant (not the sRGB one) when DetectColorspace is
// confident enough.
//
// The fixture uses varying ink-fill fractions (black pixels on a white
// background) within each 8×8 block — the same approach as the detect_test
// fixtures — so that linear vs sRGB block averaging produce detectably different
// block means. DetectColorspace identifies the mode from the ratio of per-block
// delta_from_linear vs delta_to_linear, which is ≈1.44 for linear averaging
// and ≈1.04 for sRGB averaging (threshold 1.4).
//
// The assertion is BEHAVIORAL: after applyAutoFingerprint installs the
// pixelator, run BOTH installed and reference pixelators on a fresh half-0 /
// half-255 probe block. Linear and sRGB averaging of {0, 255} produce
// measurably different grey levels (~188 vs 127), so the output pin-points
// which variant was installed.
func TestRecover_autoFingerprintInstallsLinear(t *testing.T) {
	const (
		w, h  = 64, 64
		block = 8
	)
	// Build a "varying fill" source: each block column has a different ink fill
	// fraction (1/(n+1) … n/(n+1)) with black ink on white, identical to the
	// makeVaryingFillSrc fixture used by the internal detect_test suite. This
	// guarantees blocks span the full luminance range so DetectColorspace is
	// confident and the ratio discriminator (≥1.4 → linear) fires.
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	nBlockCols := w / block
	for y := range h {
		for x := range w {
			bx := x / block
			fill := float64(bx+1) / float64(nBlockCols+1) // ink fraction: 1/9 … 8/9
			inInk := float64(x%block)/float64(block) < fill
			i := src.PixOffset(x, y)
			if inInk {
				src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 0, 0, 0, 255
			} else {
				src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = 255, 255, 255, 255
			}
		}
	}
	fixture := pixelate.NewLinearBlockAverage(block).Pixelate(src, 0, 0)

	cfg := Config{BlockSize: block}
	WithAuto()(&cfg)
	applyAutoFingerprint(&cfg, fixture)

	// Type-check: must be *pixelate.BlockAverage.
	if _, ok := cfg.Pixelator.(*pixelate.BlockAverage); !ok {
		t.Errorf("Pixelator = %T, want *pixelate.BlockAverage", cfg.Pixelator)
		return
	}

	// Behavioral check: construct a single 8×8 probe block whose left half is
	// pure black (0) and right half is pure white (255). Linear and sRGB
	// averaging of {0, 255} give different grey levels:
	//   linear: linearToSrgb8(mean(0, 1))  ≈ 188
	//   sRGB:   avg8(0+255, 2)             = 127
	// So the installed pixelator's output reveals which variant was selected.
	probe := image.NewRGBA(image.Rect(0, 0, block, block))
	for y := range block {
		for x := range block {
			i := probe.PixOffset(x, y)
			c := byte(0)
			if x >= block/2 {
				c = 255
			}
			probe.Pix[i], probe.Pix[i+1], probe.Pix[i+2], probe.Pix[i+3] = c, c, c, 255
		}
	}

	gotPix := cfg.Pixelator.(*pixelate.BlockAverage).Pixelate(probe, 0, 0)
	linearPix := pixelate.NewLinearBlockAverage(block).Pixelate(probe, 0, 0)
	srgbPix := pixelate.NewBlockAverage(block).Pixelate(probe, 0, 0)

	// Sample the centre of the single block to read the averaged colour.
	cx, cy := block/2-1, block/2-1
	got := gotPix.RGBAAt(cx, cy).R
	wantLinear := linearPix.RGBAAt(cx, cy).R
	wantSRGB := srgbPix.RGBAAt(cx, cy).R

	if wantLinear == wantSRGB {
		// Probe cannot distinguish the two variants — update probe values.
		t.Errorf("probe indistinguishable: linear R=%d == sRGB R=%d (update probe)", wantLinear, wantSRGB)
	}
	if got != wantLinear {
		t.Errorf("installed pixelator output R=%d, want linear R=%d (sRGB would be R=%d): linear variant not installed", got, wantLinear, wantSRGB)
	}
}

// TestRecover_autoDoesNotMisrouteMosaicScreenshot is the I1 regression guard:
// a whole-image mosaic screenshot (hello-world.png — a clean GIMP pixelate
// mosaic) must NOT be delegated to RecoverBlurred when WithAuto() is used.
//
// Before the fix, DetectBlur measured intra-block variance over the uncropped
// frame, classified the sharp surround as "blur" (Conf up to 1.00) and silently
// misrouted to the blur pipeline. The fix adds two guards:
//
//   - Guard 1 (exact grid veto): InferBlockGrid detects a regular axis-aligned
//     lattice in clean mosaics; Gaussian blur never produces one. When it fires,
//     delegation to RecoverBlurred is skipped in BOTH the confident branch and the
//     ambiguous meta band — so hello-world.png (gridOK=true, Conf.Kind=0.587)
//     never enters σ-sweep trials regardless of which band its Conf.Kind falls in.
//   - Guard 2 (high-confidence threshold): Conf.Kind must reach ≥ 0.95 before
//     delegating. The sharp surround in a raw screenshot depresses Conf.Kind;
//     true Gaussian-blur fixtures consistently hit 1.00.
//
// The test has two layers:
//
//  1. Predicate checks (both fixtures): InferBlockGrid and Fingerprint assertions
//     verify the raw detector state that underpins the guards.
//  2. Live Recover assertion (hello-world only): calls Recover(WithAuto()) on the
//     actual fixture and asserts BlurSigma==0 — the mosaic/default path leaves
//     BlurSigma at its zero value; only RecoverBlurred sets it non-zero. This
//     exercises the live control flow, not just the detector predicates.
//     marx.png is kept as a predicate check only (full Recover is slow on JPEG).
func TestRecover_autoDoesNotMisrouteMosaicScreenshot(t *testing.T) {
	cases := []struct {
		path        string
		wantGridOK  bool // InferBlockGrid must find a grid (Guard 1 active)
		liveRecover bool // run a full Recover and assert BlurSigma==0
		description string
	}{
		{
			path:        "testdata/real/hello-world.png",
			wantGridOK:  true,
			liveRecover: true,
			description: "clean GIMP pixelate — Guard 1 (exact grid) must veto blur delegation in both confident and ambiguous branches",
		},
		{
			path:        "testdata/real/marx.png",
			wantGridOK:  true,
			liveRecover: false,
			description: "partial-edge-block mosaic — Guard 1 (exact grid, fixed for non-zero phase) must veto blur delegation",
		},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			f, err := os.Open(tc.path) // #nosec G304 -- compile-time constant test paths
			if err != nil {
				t.Fatalf("open fixture %s: %v", tc.path, err)
			}
			img, decErr := png.Decode(f)
			_ = f.Close()
			if decErr != nil {
				t.Fatalf("decode fixture %s: %v", tc.path, decErr)
			}

			// Layer 1a — Guard 1 predicate: verify InferBlockGrid behaves as expected.
			_, gridOK := InferBlockGrid(img)
			if gridOK != tc.wantGridOK {
				t.Errorf("InferBlockGrid ok=%v, want %v on %s (%s)",
					gridOK, tc.wantGridOK, tc.path, tc.description)
			}

			// Layer 1b — Guard 2 predicate: mirror the exact delegation predicate
			// Recover evaluates (unpixel.go): delegate only when no exact grid is
			// found (Guard 1) AND DetectBlur is highly confident it is blur (Guard 2,
			// ≥ 0.95). Asserting the composed predicate is false catches a regression
			// in either guard — e.g. lowering the 0.95 threshold below marx's 0.87.
			op := forensics.Fingerprint(imutil.ToRGBA(img), forensics.Hint{Block: 0})
			wouldDelegate := !gridOK && op.Kind == forensics.KindBlur && op.Conf.Kind >= 0.95
			if wouldDelegate {
				t.Errorf("%s would misroute to blur: gridOK=%v, Kind=%v, Conf.Kind=%.2f — %s",
					tc.path, gridOK, op.Kind, op.Conf.Kind, tc.description)
			}

			if !tc.liveRecover {
				return
			}

			// Layer 2 — live Recover assertion: call the real Recover+WithAuto() and
			// verify BlurSigma==0. The mosaic/default path never sets BlurSigma;
			// only RecoverBlurred sets it non-zero. A non-zero value here means the
			// image was misrouted to the blur pipeline — a Guard 1 regression.
			//
			// WithCharset and WithMaxLength are kept minimal so the test is fast;
			// correctness of the recovered text is not the assertion here.
			res, recErr := Recover(t.Context(), img,
				WithAuto(),
				WithCharset("Hello World !"),
				WithMaxLength(15),
			)
			if recErr != nil {
				t.Fatalf("Recover+WithAuto on %s: %v", tc.path, recErr)
			}
			t.Logf("Recover+WithAuto: BestGuess=%q BestTotal=%.4f BlurSigma=%.2f",
				res.BestGuess, res.BestTotal, res.BlurSigma)
			if res.BlurSigma != 0 {
				t.Errorf("BlurSigma=%.2f on %s, want 0 — image was misrouted to the blur pipeline (Guard 1 regression)",
					res.BlurSigma, tc.path)
			}
		})
	}
}

// TestRecover_autoEqualsManualBlur is the §2.3 success criterion: it asserts
// that Recover(ctx, img, WithAuto(), …) produces the same BestGuess as the
// manual RecoverBlurred(ctx, img, …) path on the same Gaussian-blur fixture.
//
// Fixture: testdata/blur/blur_go_s2.png — the smallest committed blur sample
// (text "go", true σ=2, charset "go abcde"). It is chosen because DetectBlur
// has high confidence on it, so WithAuto() should reliably route to the blur
// path, and it completes in < 5 s on any CI box.
//
// Success criterion §2.3: when WithAuto() detects a Gaussian-blur redaction,
// Recover must delegate to the dedicated blur pipeline (beam search + σ-sweep)
// rather than run the mosaic engine — so it yields the same BestGuess as a
// manual RecoverBlurred call. Recover routes confident KindBlur inputs through
// RecoverBlurred for exactly this reason.
func TestRecover_autoEqualsManualBlur(t *testing.T) {
	const (
		fixturePath = "testdata/blur/blur_go_s2.png"
		charset     = "go abcde"
		maxLen      = 3
	)
	style := Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

	f, err := os.Open(fixturePath) // #nosec G304 -- compile-time constant test path
	if err != nil {
		t.Fatalf("open fixture %s: %v", fixturePath, err)
	}
	img, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("decode fixture %s: %v", fixturePath, err)
	}

	ctx := t.Context()

	// Manual path: RecoverBlurred with σ-sweep + beam search.
	resManual, err := RecoverBlurred(ctx, img,
		WithCharset(charset),
		WithMaxLength(maxLen),
		WithStyle(style),
	)
	if err != nil {
		t.Fatalf("RecoverBlurred: %v", err)
	}
	t.Logf("RecoverBlurred: BestGuess=%q BestTotal=%.4f BlurSigma=%.2f",
		resManual.BestGuess, resManual.BestTotal, resManual.BlurSigma)

	// Auto path: Recover + WithAuto() — should detect blur and produce the same
	// BestGuess as the manual path.
	resAuto, err := Recover(ctx, img,
		WithAuto(),
		WithCharset(charset),
		WithMaxLength(maxLen),
		WithStyle(style),
	)
	if err != nil {
		t.Fatalf("Recover+WithAuto: %v", err)
	}
	t.Logf("Recover+WithAuto: BestGuess=%q BestTotal=%.4f",
		resAuto.BestGuess, resAuto.BestTotal)

	// §2.3 criterion: the auto path must recover the same text as the manual
	// blur path. A mismatch means WithAuto() does not fully delegate to the blur
	// recovery pipeline.
	if resAuto.BestGuess != resManual.BestGuess {
		t.Errorf("§2.3 gap: Recover+WithAuto BestGuess=%q, RecoverBlurred BestGuess=%q — auto path does not delegate to the blur pipeline",
			resAuto.BestGuess, resManual.BestGuess)
	}
}
