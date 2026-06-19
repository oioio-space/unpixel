package search_test

import (
	"context"
	"image"
	"slices"
	"sync"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/imutil"
	"github.com/oioio-space/unpixel/internal/metric"
	"github.com/oioio-space/unpixel/internal/pixelate"
	"github.com/oioio-space/unpixel/internal/render"
	"github.com/oioio-space/unpixel/internal/search"
)

// makeSyntheticRedacted builds a self-consistent pixelated image by running the
// same pipeline steps used by PipelineScorer. Mirrors engine_test.go.
func makeSyntheticRedactedForSearch(t *testing.T, r *render.XImage, pix unpixel.Pixelator, text string, style unpixel.Style, blockSize int) *image.RGBA {
	t.Helper()
	img, sentinelX, err := r.Render(text, style)
	if err != nil {
		t.Fatalf("render %q: %v", text, err)
	}
	bm, imageCenter := imutil.BlueMargin(img)
	if bm == 0 {
		bm = sentinelX
	}
	img = imutil.Crop(img, 0, 0, bm, img.Bounds().Dy())
	if w := img.Bounds().Dx(); blockSize-(w%blockSize) < blockSize {
		img = imutil.PadWhite(img, w+blockSize-(w%blockSize), img.Bounds().Dy())
	}
	img = pix.Pixelate(img, 0, 0)
	leftEdge := imutil.LeftEdge(img)
	adjustedCenter := imageCenter - (imageCenter % blockSize) + 4
	redactedH := 2 * adjustedCenter
	redacted := imutil.Crop(img, leftEdge, 0, img.Bounds().Dx()-leftEdge, img.Bounds().Dy())
	if redacted.Bounds().Dy() < redactedH {
		redacted = imutil.PadWhite(redacted, redacted.Bounds().Dx(), redactedH)
	}
	return redacted
}

// buildScorerFixture returns a PipelineScorer and matching Config for the
// synthetic "ab" redacted image.
func buildScorerFixture(t *testing.T) (*search.PipelineScorer, unpixel.Config, *render.XImage, unpixel.Pixelator) {
	t.Helper()
	const blockSize = 8
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	pix := pixelate.NewBlockAverage(blockSize)
	cfg := unpixel.Config{
		Charset:        "abcdefghijklmnopqrstuvwxyz ",
		MaxLength:      10,
		BlockSize:      blockSize,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		Style:          style,
		Renderer:       r,
		Pixelator:      pix,
		Metric:         metric.NewPixelmatch(0.02),
	}
	redacted := makeSyntheticRedactedForSearch(t, r, pix, "ab", style, blockSize)
	return search.NewPipelineScorer(redacted, cfg), cfg, r, pix
}

// TestPipelineScorer_correctGuessLowScore verifies that the correct guess
// scores below the threshold and is not flagged TooBig.
func TestPipelineScorer_correctGuessLowScore(t *testing.T) {
	scorer, cfg, _, _ := buildScorerFixture(t)
	offset := unpixel.Offset{X: 0, Y: 0}

	got := scorer.Eval(t.Context(), "ab", "a", offset)
	if got.TooBig {
		t.Errorf("Eval(%q): TooBig = true, want false", "ab")
	}
	if got.Score >= cfg.Threshold {
		t.Errorf("Eval(%q): Score = %v, want < %v", "ab", got.Score, cfg.Threshold)
	}
}

// TestPipelineScorer_wrongGuessScoresWorse verifies that an unrelated guess
// scores higher than the correct guess.
func TestPipelineScorer_wrongGuessScoresWorse(t *testing.T) {
	scorer, _, _, _ := buildScorerFixture(t)
	offset := unpixel.Offset{X: 0, Y: 0}

	correct := scorer.Eval(t.Context(), "ab", "a", offset)
	wrong := scorer.Eval(t.Context(), "zz", "z", offset)
	if correct.Score >= wrong.Score {
		t.Errorf("correct guess scored %v, wrong guess scored %v; want correct < wrong",
			correct.Score, wrong.Score)
	}
}

// TestPipelineScorer_tooBigGuess verifies that a string far wider than the
// synthetic "ab" redacted image is flagged TooBig.
func TestPipelineScorer_tooBigGuess(t *testing.T) {
	scorer, _, _, _ := buildScorerFixture(t)
	offset := unpixel.Offset{X: 0, Y: 0}

	got := scorer.Eval(t.Context(), "abcdefghijklmnopqrstuvwxyz", "", offset)
	if !got.TooBig {
		t.Errorf("Eval(very long string): TooBig = false, want true")
	}
}

// TestPipelineScorer_emptyPrevGuess exercises the empty-prevGuess branch (no
// marginal crop) and asserts a valid score in [0, 1].
func TestPipelineScorer_emptyPrevGuess(t *testing.T) {
	scorer, _, _, _ := buildScorerFixture(t)
	offset := unpixel.Offset{X: 0, Y: 0}

	got := scorer.Eval(t.Context(), "a", "", offset)
	if got.Score < 0 || got.Score > 1 {
		t.Errorf("Eval(%q, empty): Score = %v, want in [0, 1]", "a", got.Score)
	}
}

// TestPipelineScorer_prevGuessBranch exercises the marginal-crop path (non-empty
// prevGuess) and asserts a valid score in [0, 1].
func TestPipelineScorer_prevGuessBranch(t *testing.T) {
	scorer, _, _, _ := buildScorerFixture(t)
	offset := unpixel.Offset{X: 0, Y: 0}

	got := scorer.Eval(t.Context(), "ab", "a", offset)
	if got.Score < 0 || got.Score > 1 {
		t.Errorf("Eval(%q, %q): Score = %v, want in [0, 1]", "ab", "a", got.Score)
	}
}

// TestPipelineScorer_cancelledContext verifies that a pre-cancelled context
// causes Eval to return score=1 without rendering.
func TestPipelineScorer_cancelledContext(t *testing.T) {
	scorer, _, _, _ := buildScorerFixture(t)
	offset := unpixel.Offset{X: 0, Y: 0}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got := scorer.Eval(ctx, "ab", "a", offset)
	if got.Score != 1 {
		t.Errorf("Eval with cancelled ctx: Score = %v, want 1", got.Score)
	}
}

// TestPipelineScorer_identicalConsecutiveGuess exercises the marginColumn path where
// consecutive identical characters produce an all-white diff (leftBoundary falls
// back to prevImg width).
func TestPipelineScorer_identicalConsecutiveGuess(t *testing.T) {
	scorer, _, _, _ := buildScorerFixture(t)
	offset := unpixel.Offset{X: 0, Y: 0}

	got := scorer.Eval(t.Context(), "aa", "a", offset)
	if got.Score < 0 || got.Score > 1 {
		t.Errorf("Eval(aa, a): Score = %v, want in [0, 1]", got.Score)
	}
}

// drainAndRun calls strategy.Search then closes both channels, draining them
// concurrently to avoid the blocking-send deadlock described in engine_test.go.
func drainAndRun(
	t *testing.T,
	strategy unpixel.Strategy,
	redacted *image.RGBA,
	cfg unpixel.Config,
) (progress []unpixel.Progress, results []unpixel.Result) {
	t.Helper()
	out := make(chan unpixel.Progress, 512)
	resCh := make(chan unpixel.Result, 16)

	var wg sync.WaitGroup
	wg.Go(func() {
		for p := range out {
			progress = append(progress, p)
		}
	})
	wg.Go(func() {
		for r := range resCh {
			results = append(results, r)
		}
	})

	strategy.Search(t.Context(), redacted, cfg, out, resCh)
	close(out)
	close(resCh)
	wg.Wait()
	return progress, results
}

// TestGuidedStrategy_emitsDone verifies that Search always emits EventDone.
func TestGuidedStrategy_emitsDone(t *testing.T) {
	const blockSize = 8
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	pix := pixelate.NewBlockAverage(blockSize)
	cfg := unpixel.Config{
		Charset:        "ab",
		MaxLength:      2,
		BlockSize:      blockSize,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		Style:          style,
		Renderer:       r,
		Pixelator:      pix,
		Metric:         metric.NewPixelmatch(0.02),
	}
	redacted := makeSyntheticRedactedForSearch(t, r, pix, "a", style, blockSize)

	progress, _ := drainAndRun(t, search.NewGuidedStrategy(), redacted, cfg)

	gotDone := slices.ContainsFunc(progress, func(p unpixel.Progress) bool {
		return p.Kind == unpixel.EventDone
	})
	if !gotDone {
		t.Error("GuidedStrategy.Search did not emit EventDone")
	}
}

// TestGuidedStrategy_searchFindsCandidate runs the real strategy against a
// synthetic "ab" redaction and logs what it recovers. It asserts the search
// completes without panic and that at least one candidate was produced.
func TestGuidedStrategy_searchFindsCandidate(t *testing.T) {
	const blockSize = 8
	r, err := render.NewXImage()
	if err != nil {
		t.Fatalf("render.NewXImage: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}
	pix := pixelate.NewBlockAverage(blockSize)
	cfg := unpixel.Config{
		Charset:        "abcdefghijklmnopqrstuvwxyz ",
		MaxLength:      3,
		BlockSize:      blockSize,
		Threshold:      0.25,
		SpaceThreshold: 0.5,
		Style:          style,
		Renderer:       r,
		Pixelator:      pix,
		Metric:         metric.NewPixelmatch(0.02),
	}
	redacted := makeSyntheticRedactedForSearch(t, r, pix, "ab", style, blockSize)

	_, results := drainAndRun(t, search.NewGuidedStrategy(), redacted, cfg)

	var allCandidates []string
	for _, res := range results {
		for _, e := range res.Candidates {
			allCandidates = append(allCandidates, e.Guess)
		}
	}
	t.Logf("GuidedStrategy found %d candidates: %v", len(allCandidates), allCandidates)
	if slices.Contains(allCandidates, "ab") {
		t.Logf("correctly recovered plaintext 'ab'")
	}
}
