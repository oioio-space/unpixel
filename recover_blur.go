package unpixel

import (
	"context"
	"image"
	"math"
	"runtime"
	"sync"

	"github.com/oioio-space/unpixel/internal/deblur"
	"github.com/oioio-space/unpixel/internal/pixelate"
)

// RecoverBlurred recovers text from a Gaussian-blurred redaction without being
// told the blur standard deviation σ. It is the zero-config counterpart of
// Recover with a manual WithPixelator(GaussianBlur(σ)) + WithBlockSize(1).
//
// # Default search strategy: beam search
//
// RecoverBlurred defaults to beam search (BeamStrategy) rather than the guided
// DFS used by Recover. Behind blur the per-character image signal is weak: the
// correct character's score often beats the wrong one by only a small margin,
// and guided DFS expands every passing branch depth-first — producing O(|charset|^length)
// evaluations for longer words, which makes it impractical for ≥5-character
// targets. Beam search bounds work to O(length × BeamWidth) evaluations,
// making even 7-character recoveries finish in milliseconds.
//
// The effective BeamWidth for blur recovery is 32 (set by
// defaults.DefaultBlurStrategy); DefaultBeamWidth = 16 is only the generic
// BeamStrategy fallback. Either value is wider than the typical blur charset,
// giving full per-level coverage for small alphabets. Combine with
// WithLanguageModel (e.g. WithLanguageModel(lang.PriorFor(lang.English))) for
// disambiguation when the image signal alone does not separate candidates.
//
// Callers that need full guided-DFS recall can override with
// WithStrategy(defaults.GuidedStrategy()), which also disables the beam
// default.
//
// # Algorithm (adaptive σ-search)
//
// Gaussian blur is a known, deterministic forward operator B_σ. The
// generate-and-test attack (render → blur → compare) inverts it: render a
// candidate, blur with the same σ, measure image distance — the true text
// will have the lowest distance.
//
// RecoverBlurred uses an adaptive strategy that minimises the number of full
// Recover calls to the common case of ONE:
//
//  1. Estimate σ₀ = InferBlurSigma(img). The estimator is accurate to within
//     ~15% on typical text images (validated over the fixture set).
//  2. Run ONE full Recover at σ₀ with exact GaussianBlur.
//  3. If Result.BestGuess is non-empty and Result.BestTotal < blurAcceptThreshold
//     the early-accept gate fires: σ₀ is close enough and the candidate is
//     convincing — return immediately (the common case, ≈1× single Recover).
//  4. Otherwise, expand to a bounded fallback sweep: build a coarse grid of ±2
//     steps around σ₀ (4 extra σ candidates at multipliers {0.7, 0.85, 1.18,
//     1.4}×σ₀, or a fixed wide grid when σ₀=0), run them in parallel, then
//     fine-refine (±0.25, 5 steps, sequential) around the best coarse winner.
//     This fallback adds ≤9 extra Recover calls — preserving robustness for the
//     minority of images where σ₀ is off by more than ~15%.
//
// # Accept threshold (blurAcceptThreshold)
//
// BestTotal is the whole-image pixel distance (in [0,1]) of the best guess
// against the redaction. For a correct recovery at near-exact σ, BestTotal is
// typically 0.01–0.04 (pixel-perfect near-match). An incorrect recovery or a
// σ mismatch produces BestTotal ≫ 0.08. The threshold 0.07 sits comfortably
// above the correct-recovery floor and below the wrong-σ ceiling, giving a
// robust early-exit signal without a recall risk.
//
// # Concurrency model
//
// The initial single-σ search uses full Workers parallelism (same as a plain
// Recover call). The fallback sweep uses the same bounded-pool strategy as the
// old implementation: each σ gets 1 inner worker; up to NumCPU run at once.
//
// All caller-supplied opts (WithCharset, WithMaxLength, WithStyle,
// WithLanguageModel, WithWorkers, WithStrategy, …) are forwarded to every
// inner Recover call; WithBlockSize and WithPixelator are set internally and
// override any value the caller passes.
//
// Ctx cancellation is respected; a cancelled sweep returns the best result
// found so far.
func RecoverBlurred(ctx context.Context, img image.Image, opts ...Option) (Result, error) {
	if img == nil {
		return Result{}, ErrNilImage
	}

	// Prepend beam search as the default strategy for blur when the caller has
	// not supplied WithStrategy or WithBeamWidth. Beam search is vastly more
	// practical than guided DFS for blurred text: it bounds work to
	// O(length × BeamWidth) regardless of charset size, so a 7-character word
	// with a 10-char charset takes ~70 evaluations instead of 10^7.
	//
	// We detect a caller-supplied strategy by probing a zero Config: if opts
	// leave cfg.Strategy nil after application, no override was given.
	opts = blurWithBeamDefault(opts)

	// Input normalisation (opt-in via WithNormalize). Probe the opts to check
	// whether the caller passed WithNormalize; if so, apply it to the image
	// before σ estimation and the search. This does NOT affect the default path:
	// a nil normalize field leaves img untouched and Result.Normalized = false.
	var normalized bool
	{
		var probe Config
		for _, o := range opts {
			o(&probe)
		}
		if probe.normalize != nil {
			img = deblur.Normalize(toRGBA(img), *probe.normalize)
			normalized = true
		}
	}

	σ0 := InferBlurSigma(img)

	// Fast path: try σ₀ first with full parallelism.
	// For most images InferBlurSigma is accurate to ~15%, which is close
	// enough for the search to find the correct text on the first attempt.
	if σ0 > 0 {
		if res, err := recoverAtSigma(ctx, img, σ0, false, opts); err != nil {
			return Result{}, err
		} else if ctx.Err() == nil && res.BestGuess != "" && res.BestTotal < blurAcceptThreshold {
			// Early-accept: σ₀ is good — no sweep needed.
			res.BlurSigma = σ0
			res.Normalized = normalized
			return res, nil
		}
	}

	// Fallback sweep: σ₀ was 0 (flat image) or the single search failed the
	// accept gate (σ₀ off by more than ~15%). Run the coarse+fine sweep.
	coarse := blurSigmaGrid(σ0)
	bestResult, bestSigma, err := sweepSigmasParallel(ctx, img, coarse, true, opts)
	if err != nil {
		return Result{}, err
	}
	if ctx.Err() != nil {
		bestResult.BlurSigma = bestSigma
		bestResult.Normalized = normalized
		return bestResult, nil
	}

	// Fine-refine around the coarse winner: ±0.25 in 5 steps.
	// Use exact GaussianBlur: the fine pass fixes σ estimation accuracy and
	// produces the returned Result, so fidelity matters here.
	fine := fineSigmaGrid(bestSigma)
	fineResult, fineSigma, err := sweepSigmasSeq(ctx, img, fine, opts)
	if err != nil {
		return Result{}, err
	}

	if fineResult.BestGuess != "" && (bestResult.BestGuess == "" || fineResult.BestTotal < bestResult.BestTotal) {
		bestResult = fineResult
		bestSigma = fineSigma
	}

	bestResult.BlurSigma = bestSigma
	bestResult.Normalized = normalized
	return bestResult, nil
}

// blurAcceptThreshold is the BestTotal gate for the early-accept path. When
// the initial single-σ search produces a BestTotal below this value the result
// is considered convincing and RecoverBlurred returns immediately without
// running the fallback sweep.
//
// Correct recoveries at near-exact σ consistently score BestTotal ≈ 0.01–0.04.
// Wrong-σ or wrong-text recoveries score ≥ 0.10. The threshold 0.07 sits
// in the gap, giving a robust accept signal with no recall risk.
const blurAcceptThreshold = 0.07

// recoverAtSigma runs a single Recover at the given σ. When fast is true and
// σ ≥ blurFastMinSigma it uses NewFastBlur (ranking-preserving, cheaper);
// otherwise it uses exact GaussianBlur. The Workers option in opts is forwarded
// unchanged (full parallelism for the fast path, 1 worker for the sweep path).
func recoverAtSigma(ctx context.Context, img image.Image, σ float64, fast bool, opts []Option) (Result, error) {
	blur := resolveBlurOp(σ, fast)
	runOpts := make([]Option, 0, len(opts)+2)
	runOpts = append(runOpts, opts...)
	runOpts = append(runOpts, WithBlockSize(1), WithPixelator(blur))
	return Recover(ctx, img, runOpts...)
}

// blurFastMinSigma is the σ threshold at/above which the coarse sweep uses
// NewFastBlur (O(1) box approximation) instead of GaussianBlur. Below this
// threshold the exact operator is already cheap; above it FastBlur is 3–5×
// faster while preserving candidate ranking (validated by the blur matrix).
const blurFastMinSigma = 3.0

// resolveBlurOp returns the blur Pixelator for one σ candidate. fast=true
// selects the O(1) box approximation for σ ≥ blurFastMinSigma (coarse sweep);
// fast=false always uses the exact Gaussian (fine sweep and returned result).
func resolveBlurOp(σ float64, fast bool) Pixelator {
	if fast && σ >= blurFastMinSigma {
		return pixelate.NewFastBlur(σ)
	}
	return pixelate.NewGaussianBlur(σ)
}

// blurSigmaMin and blurSigmaMax are the σ bounds shared by the coarse and fine
// sigma grids. They bracket the practical Gaussian-blur range for text images:
// σ < 0.5 is visually near-sharp; σ > 20 is so heavy it destroys all structure.
const (
	blurSigmaMin = 0.5
	blurSigmaMax = 20.0
)

// blurSigmaGrid builds the coarse σ candidate set. When σ0 > 0 it returns 4
// values at {0.7, 0.85, 1.18, 1.4}×σ0 clamped to [blurSigmaMin, blurSigmaMax]
// (the centre σ0 is tried first in the fast path, so the grid contains only
// the neighbours). When σ0 is 0 (InferBlurSigma returned nothing useful) it
// returns a fixed fallback grid covering the typical blur range.
func blurSigmaGrid(σ0 float64) []float64 {
	if σ0 <= 0 {
		// Flat / unrecognised image — try a wide fixed grid.
		return []float64{1, 2, 3, 4, 6}
	}
	// Neighbours only: σ0 itself was already tried in the fast path.
	mults := [4]float64{0.7, 0.85, 1.18, 1.4}
	grid := make([]float64, 0, len(mults))
	seen := map[float64]bool{}
	for _, m := range mults {
		σ := math.Round(σ0*m*100) / 100 // 2-decimal rounding to deduplicate
		σ = max(blurSigmaMin, min(blurSigmaMax, σ))
		if !seen[σ] {
			grid = append(grid, σ)
			seen[σ] = true
		}
	}
	return grid
}

// fineSigmaGrid builds the ±0.25 refinement grid around σ_best.
func fineSigmaGrid(σBest float64) []float64 {
	deltas := [5]float64{-0.25, -0.125, 0, 0.125, 0.25}
	grid := make([]float64, 0, len(deltas))
	seen := map[float64]bool{}
	for _, d := range deltas {
		σ := math.Round((σBest+d)*100) / 100
		σ = max(blurSigmaMin, min(blurSigmaMax, σ))
		if !seen[σ] {
			grid = append(grid, σ)
			seen[σ] = true
		}
	}
	return grid
}

// sweepResult is the outcome of one σ candidate evaluation.
// skipped is true when the goroutine exited early due to ctx cancellation
// before running Recover; such slots carry no meaningful res/sigma/rank and
// are excluded from winner selection.
type sweepResult struct {
	res     Result
	sigma   float64
	rank    float64
	skipped bool
}

// sweepSigmasParallel runs Recover for each σ in grid concurrently (bounded
// pool) and returns the best Result and its σ. When fastBlur is true the coarse
// sweep uses NewFastBlur for σ ≥ blurFastMinSigma — cheaper and ranking-
// preserving, so it correctly identifies the σ region without needing exact
// pixel fidelity. Each inner Recover is given a single offset worker so the
// total CPU usage equals NumCPU regardless of the number of σ candidates.
func sweepSigmasParallel(ctx context.Context, img image.Image, grid []float64, fastBlur bool, opts []Option) (Result, float64, error) {
	if len(grid) == 0 {
		return Result{}, 0, nil
	}

	// CPU budget: each σ gets 1 inner worker; up to NumCPU run concurrently.
	numCPU := runtime.GOMAXPROCS(0)
	concurrent := max(1, min(len(grid), numCPU))

	results := make([]sweepResult, len(grid))
	errs := make([]error, len(grid))
	sem := make(chan struct{}, concurrent)
	var wg sync.WaitGroup
	for i, σ := range grid {
		σ := σ
		i := i
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				results[i] = sweepResult{skipped: true}
				return
			}
			blur := resolveBlurOp(σ, fastBlur)
			runOpts := make([]Option, 0, len(opts)+3)
			runOpts = append(runOpts, opts...)
			runOpts = append(runOpts, WithBlockSize(1), WithPixelator(blur), WithWorkers(1))

			res, err := Recover(ctx, img, runOpts...)
			if err != nil {
				errs[i] = err
				return
			}
			rank := 1.0
			if res.BestGuess != "" {
				rank = res.BestTotal
			}
			results[i] = sweepResult{res: res, sigma: σ, rank: rank}
		})
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return Result{}, 0, err
		}
	}

	// Select minimum rank; tie-break by σ (smaller σ first for determinism).
	best := sweepResult{rank: 2.0} // outside [0,1]; worse than any real score
	found := false
	for _, sr := range results {
		if sr.skipped {
			continue // ctx was cancelled before this slot ran
		}
		if !found || sr.rank < best.rank || (sr.rank == best.rank && sr.sigma < best.sigma) {
			best = sr
			found = true
		}
	}
	if !found {
		return Result{}, 0, nil
	}
	return best.res, best.sigma, nil
}

// sweepSigmasSeq runs Recover for each σ in grid sequentially with exact
// GaussianBlur and returns the best Result and its σ. It is used for the fine
// pass (narrow grid, needs exact fidelity) and respects ctx cancellation.
func sweepSigmasSeq(ctx context.Context, img image.Image, grid []float64, opts []Option) (Result, float64, error) {
	var (
		best      Result
		bestSigma float64
		bestRank  = 2.0 // outside [0, 1]; worse than any real score
		found     bool
	)

	for _, σ := range grid {
		if ctx.Err() != nil {
			break
		}
		blur := pixelate.NewGaussianBlur(σ)
		runOpts := make([]Option, 0, len(opts)+2)
		runOpts = append(runOpts, opts...)
		runOpts = append(runOpts, WithBlockSize(1), WithPixelator(blur))

		res, err := Recover(ctx, img, runOpts...)
		if err != nil {
			return Result{}, 0, err
		}

		// Score by BestTotal: whole-image distance of the best guess; prefer
		// lower. A run that produced no guess is treated as worst (rank=1).
		rank := 1.0
		if res.BestGuess != "" {
			rank = res.BestTotal
		}
		if !found || rank < bestRank {
			best = res
			bestSigma = σ
			bestRank = rank
			found = true
		}
	}

	return best, bestSigma, nil
}

// blurWithBeamDefault prepends a WithStrategy(DefaultBlurStrategy()) option
// when the caller has not already supplied a WithStrategy override. It detects
// a Strategy override by probing a zero Config: if cfg.Strategy is still nil
// after applying opts, no WithStrategy was passed, and beam search is injected
// as the default.
//
// WithBeamWidth alone does NOT suppress the default — it adjusts the width of
// the injected beam (because the caller's opts are applied after the prepended
// WithStrategy, WithBeamWidth will take effect normally).
//
// When DefaultBlurStrategy is nil (defaults package not imported) the opts
// slice is returned unchanged; the existing DefaultComponents fallback will
// wire GuidedDFS as usual.
func blurWithBeamDefault(opts []Option) []Option {
	if DefaultBlurStrategy == nil {
		return opts
	}
	// Probe: check whether any opt explicitly sets Strategy.
	var probe Config
	for _, opt := range opts {
		opt(&probe)
	}
	if probe.Strategy != nil {
		// Caller explicitly chose a strategy — respect it.
		return opts
	}
	// Prepend so the caller's opts (applied after) can still override width etc.
	beam := WithStrategy(DefaultBlurStrategy())
	result := make([]Option, 0, len(opts)+1)
	result = append(result, beam)
	result = append(result, opts...)
	return result
}
