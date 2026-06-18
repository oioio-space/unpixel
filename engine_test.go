// Package unpixel_test contains end-to-end tests for the Engine, including the
// self-redaction round-trip that validates core pipeline correctness.
package unpixel_test

import (
	"context"
	"image"
	"image/png"
	"os"
	"slices"
	"sync"
	"testing"

	_ "github.com/oioio-space/unpixel/defaults" // wire default components via init()

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
)

// components holds the real pipeline implementations used across tests.
type components struct {
	renderer  unpixel.Renderer
	pixelator unpixel.Pixelator
	metric    unpixel.Metric
	strategy  unpixel.Strategy
}

// buildComponents constructs the default pipeline components for a given blockSize.
func buildComponents(t *testing.T, blockSize int) components {
	t.Helper()
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	return components{
		renderer:  r,
		pixelator: pixelate.NewBlockAverage(blockSize),
		metric:    metric.NewPixelmatch(0.02),
		strategy:  search.NewGuidedStrategy(),
	}
}

// makeSyntheticRedacted produces a self-consistent synthetic "redacted" image
// by running the same pipeline steps that PipelineScorer uses on the plaintext.
// The resulting image has height 2*adjustedCenter so that when the scorer
// evaluates the correct guess, cropY=0 and the comparison is exact.
//
// Steps mirror the faithful pipeline in DESIGN.md §Faithful pipeline steps 1-7.
func makeSyntheticRedacted(t *testing.T, c components, text string, style unpixel.Style, blockSize int) *image.RGBA {
	t.Helper()

	img, sentinelX, err := c.renderer.Render(text, style)
	if err != nil {
		t.Fatalf("makeSyntheticRedacted: render %q: %v", text, err)
	}

	// Step 2: locate blue sentinel.
	bm, imageCenter := imutil.BlueMargin(img)
	if bm == 0 {
		bm = sentinelX
	}

	// Step 3: crop to grid origin (offset 0,0 for round-trip).
	img = imutil.Crop(img, 0, 0, bm, img.Bounds().Dy())
	// imageCenter unchanged (oy=0).

	// Step 4: white-pad to block multiple.
	if w := img.Bounds().Dx(); blockSize-(w%blockSize) < blockSize {
		img = imutil.PadWhite(img, w+blockSize-(w%blockSize), img.Bounds().Dy())
	}

	// Step 5: pixelate.
	img = c.pixelator.Pixelate(img, 0, 0)

	// Step 6: leftEdge.
	leftEdge := imutil.LeftEdge(img)

	// Step 7: vertical crop.
	// Use redactedH = 2*adjustedCenter so cropY = adjustedCenter - redactedH/2 = 0.
	// This guarantees self-consistency: the scorer sees cropY=0 for the correct guess.
	adjustedCenter := imageCenter - (imageCenter % blockSize) + 4
	redactedH := 2 * adjustedCenter

	redacted := imutil.Crop(img, leftEdge, 0, img.Bounds().Dx()-leftEdge, img.Bounds().Dy())
	// Pad to redactedH height if the pixelated image is shorter.
	if redacted.Bounds().Dy() < redactedH {
		redacted = imutil.PadWhite(redacted, redacted.Bounds().Dx(), redactedH)
	}
	return redacted
}

// makeConfig returns a Config populated with the given components and sensible defaults.
func makeConfig(c components, blockSize int, maxLength int, style unpixel.Style) unpixel.Config {
	return unpixel.Config{
		Charset:        unpixel.DefaultCharset,
		MaxLength:      maxLength,
		BlockSize:      blockSize,
		Threshold:      unpixel.DefaultThreshold,
		SpaceThreshold: unpixel.DefaultSpaceThreshold,
		Style:          style,
		Renderer:       c.renderer,
		Pixelator:      c.pixelator,
		Metric:         c.metric,
		Strategy:       c.strategy,
	}
}

// TestEngine_roundTrip is the headline self-redaction test.
// It produces a synthetic "redacted" image by running the full pipeline on a
// known plaintext, then runs Engine.Run and asserts the plaintext appears in
// the candidate set.
func TestEngine_roundTrip(t *testing.T) {
	const (
		plaintext = "hello"
		blockSize = 8
	)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := buildComponents(t, blockSize)

	// Produce the synthetic redacted image using the same pipeline as the scorer,
	// so that Engine.Run sees a self-consistent target.
	redacted := makeSyntheticRedacted(t, c, plaintext, style, blockSize)

	cfg := makeConfig(c, blockSize, len(plaintext)+2, style)
	eng, err := unpixel.New(redacted, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	progCh, resultCh := eng.Run(t.Context())

	// Drain both channels concurrently — Search can block on EventNewBest
	// (blocking send to progCh) before writing to resultCh, so sequential
	// draining would deadlock if progCh fills up.
	var (
		mu            sync.Mutex
		allCandidates []string
		bestGuess     string
		gotDone       bool
	)
	var wg sync.WaitGroup
	wg.Go(func() {
		for res := range resultCh {
			if res.Err != nil {
				t.Errorf("result error: %v", res.Err)
			}
			mu.Lock()
			bestGuess = res.BestGuess
			for _, e := range res.Candidates {
				allCandidates = append(allCandidates, e.Guess)
			}
			mu.Unlock()
		}
	})
	wg.Go(func() {
		for p := range progCh {
			if p.Kind == unpixel.EventDone {
				mu.Lock()
				gotDone = true
				mu.Unlock()
			}
		}
	})
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	t.Logf("bestGuess=%q candidates(%d)=%v", bestGuess, len(allCandidates), allCandidates)

	if !gotDone {
		t.Error("progress channel closed without EventDone")
	}
	if !slices.Contains(allCandidates, plaintext) {
		t.Errorf("plaintext %q not found in candidates; bestGuess=%q", plaintext, bestGuess)
	}
}

// TestEngine_ctxCancel verifies that cancelling ctx causes both channels to
// close promptly without deadlocking.
func TestEngine_ctxCancel(t *testing.T) {
	const blockSize = 8
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := buildComponents(t, blockSize)

	redacted := makeSyntheticRedacted(t, c, "test", style, blockSize)

	cfg := makeConfig(c, blockSize, 20, style)
	eng, err := unpixel.New(redacted, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately before Run

	progCh, resultCh := eng.Run(ctx)
	for range resultCh {
	}
	for range progCh {
	}
	// Test passes if we reach here without hanging.
}

// TestEngine_progressDoneAlwaysDelivered verifies that EventDone is always the
// final event on the progress channel after a normal (non-cancelled) run.
// It also drains progCh and resultCh concurrently to avoid deadlock.
func TestEngine_progressDoneAlwaysDelivered(t *testing.T) {
	const blockSize = 8
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	c := buildComponents(t, blockSize)

	redacted := makeSyntheticRedacted(t, c, "ab", style, blockSize)

	cfg := makeConfig(c, blockSize, 5, style)
	eng, err := unpixel.New(redacted, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	progCh, resultCh := eng.Run(t.Context())

	var (
		mu      sync.Mutex
		gotDone bool
	)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range resultCh {
		}
	})
	wg.Go(func() {
		for p := range progCh {
			if p.Kind == unpixel.EventDone {
				mu.Lock()
				gotDone = true
				mu.Unlock()
			}
		}
	})
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if !gotDone {
		t.Error("EventDone not delivered after normal completion")
	}
}

// TestEngine_secretPng is a skipped Phase-2 smoke test for the Chromium-rendered
// fixture. The x/image renderer differs from Chromium's rasterizer, so recovery
// is not asserted here — that is a Phase-2 goal requiring a chromedp renderer.
func TestEngine_secretPng(t *testing.T) {
	t.Skip("Phase-2 fidelity test: Chromium-rendered secret.png requires chromedp renderer")

	f, err := os.Open("internal/testdata/secret.png")
	if err != nil {
		t.Fatalf("open secret.png: %v", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Errorf("close secret.png: %v", cerr)
		}
	}()

	decoded, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode secret.png: %v", err)
	}

	b := decoded.Bounds()
	redacted := image.NewRGBA(b)
	for y := range b.Dy() {
		for x := range b.Dx() {
			redacted.Set(x, y, decoded.At(x, y))
		}
	}

	c := buildComponents(t, 8)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	cfg := makeConfig(c, 8, 20, style)

	eng, err := unpixel.New(redacted, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	progCh, resultCh := eng.Run(t.Context())
	for range resultCh {
	}
	for range progCh {
	}
}
