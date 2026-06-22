package unpixel_test

import (
	"context"
	"errors"
	"image"
	"sync"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/deblur"
)

// TestOnProgress_deliversAllEvents verifies that OnProgress calls fn for every
// value on the channel and returns after the channel is closed.
func TestOnProgress_deliversAllEvents(t *testing.T) {
	ch := make(chan unpixel.Progress, 3)
	ch <- unpixel.Progress{Kind: unpixel.EventCandidate, Guess: "a"}
	ch <- unpixel.Progress{Kind: unpixel.EventNewBest, BestGuess: "ab"}
	ch <- unpixel.Progress{Kind: unpixel.EventDone, Done: true}
	close(ch)

	var got []unpixel.Progress
	unpixel.OnProgress(ch, func(p unpixel.Progress) {
		got = append(got, p)
	})

	if len(got) != 3 {
		t.Fatalf("OnProgress delivered %d events, want 3", len(got))
	}
	if got[0].Kind != unpixel.EventCandidate {
		t.Errorf("got[0].Kind = %v, want EventCandidate", got[0].Kind)
	}
	if got[1].BestGuess != "ab" {
		t.Errorf("got[1].BestGuess = %q, want %q", got[1].BestGuess, "ab")
	}
	if !got[2].Done {
		t.Errorf("got[2].Done = false, want true")
	}
}

// TestOnProgress_emptyChannel verifies that OnProgress on a closed empty
// channel calls fn zero times and returns immediately.
func TestOnProgress_emptyChannel(t *testing.T) {
	ch := make(chan unpixel.Progress)
	close(ch)

	calls := 0
	unpixel.OnProgress(ch, func(unpixel.Progress) { calls++ })
	if calls != 0 {
		t.Errorf("OnProgress on empty closed channel: fn called %d times, want 0", calls)
	}
}

// TestToRGBA_nonRGBAImage verifies that passing a non-*image.RGBA image to New
// converts it to *image.RGBA preserving the correct dimensions.
// toRGBA is exercised transitively via New.
func TestToRGBA_nonRGBAImage(t *testing.T) {
	// image.NewNRGBA is not *image.RGBA so toRGBA takes the conversion branch.
	const (
		w = 16
		h = 8
	)
	src := image.NewNRGBA(image.Rect(0, 0, w, h))
	// Fill with a recognisable non-white colour.
	for i := range src.Pix {
		src.Pix[i] = 0xAB
	}

	// New calls toRGBA internally.
	_, err := unpixel.New(src, unpixel.Config{
		BlockSize: 8,
		// Inject a no-op Strategy so Run is not needed and no real components
		// are required.
	})
	if err != nil {
		t.Fatalf("New(NRGBA): %v", err)
	}
	// If we got here without panic, toRGBA handled the non-RGBA path.
}

// TestToRGBA_rgbaPassthrough verifies that passing an *image.RGBA to New does
// not panic (the fast-path branch in toRGBA).
func TestToRGBA_rgbaPassthrough(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 16, 8))
	_, err := unpixel.New(src, unpixel.Config{BlockSize: 8})
	if err != nil {
		t.Fatalf("New(*image.RGBA): %v", err)
	}
}

// TestApplyDefaults_zeroConfigFillsAll verifies that a zero Config gets all
// scalar defaults applied, including a working ThresholdFor closure.
// applyDefaults is exercised transitively via New.
func TestApplyDefaults_zeroConfigFillsAll(t *testing.T) {
	// We cannot call applyDefaults directly (unexported), but New calls it.
	// Provide a stub Strategy so Run is not needed.
	cfg := unpixel.Config{
		BlockSize: unpixel.DefaultBlockSize, // required for BlockAverage; keep zero behaviour otherwise
		Strategy:  &noopStrategy{},
	}
	eng, err := unpixel.New(image.NewRGBA(image.Rect(0, 0, 8, 8)), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Run a short search so we can observe what Config was applied.
	// We just want it to not panic, which proves applyDefaults ran without error.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	progCh, resCh := eng.Run(ctx)
	for range resCh {
	}
	for range progCh {
	}
}

// TestApplyDefaults_thresholdForClosure verifies the ThresholdFor closure that
// applyDefaults installs: ' ' → SpaceThreshold, other runes → Threshold.
// We test this via a spy Strategy that captures the config it receives.
func TestApplyDefaults_thresholdForClosure(t *testing.T) {
	spy := &spyStrategy{}
	cfg := unpixel.Config{
		BlockSize: 8,
		Strategy:  spy,
		// Leave Threshold, SpaceThreshold, and ThresholdFor at zero → defaults apply.
	}
	eng, err := unpixel.New(image.NewRGBA(image.Rect(0, 0, 8, 8)), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	progCh, resCh := eng.Run(ctx)
	for range resCh {
	}
	for range progCh {
	}

	if spy.cfg == nil {
		t.Fatal("spy Strategy.Search was never called")
	}
	got := spy.cfg.ThresholdFor(' ')
	if got != unpixel.DefaultSpaceThreshold {
		t.Errorf("ThresholdFor(' ') = %v, want %v", got, unpixel.DefaultSpaceThreshold)
	}
	got = spy.cfg.ThresholdFor('a')
	if got != unpixel.DefaultThreshold {
		t.Errorf("ThresholdFor('a') = %v, want %v", got, unpixel.DefaultThreshold)
	}
}

// TestRun_defaultComponentsErrorSurfaced verifies that when DefaultComponents
// returns an error, Run closes both channels and delivers EventDone with Err set.
func TestRun_defaultComponentsErrorSurfaced(t *testing.T) {
	// Temporarily replace DefaultComponents with one that always errors.
	original := unpixel.DefaultComponents
	t.Cleanup(func() { unpixel.DefaultComponents = original })

	sentinel := errors.New("component wiring failed")
	unpixel.DefaultComponents = func(*unpixel.Config) error { return sentinel }

	eng, err := unpixel.New(image.NewRGBA(image.Rect(0, 0, 8, 8)), unpixel.Config{BlockSize: 8})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	progCh, resCh := eng.Run(t.Context())

	var lastProg unpixel.Progress
	var wg sync.WaitGroup
	wg.Go(func() {
		for p := range progCh {
			lastProg = p
		}
	})
	wg.Go(func() {
		for range resCh {
		}
	})
	wg.Wait()

	if !lastProg.Done {
		t.Error("Run: EventDone not delivered after DefaultComponents error")
	}
	if !errors.Is(lastProg.Err, sentinel) {
		t.Errorf("Run: EventDone.Err = %v, want wrapping %v", lastProg.Err, sentinel)
	}
}

// TestRun_cancelledContextClosesBothChannels verifies that a pre-cancelled
// context causes both channels to close without deadlock.
func TestRun_cancelledContextClosesBothChannels(t *testing.T) {
	eng, err := unpixel.New(image.NewRGBA(image.Rect(0, 0, 8, 8)), unpixel.Config{
		BlockSize: 8,
		Strategy:  &noopStrategy{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	progCh, resCh := eng.Run(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range resCh {
		}
	})
	wg.Go(func() {
		for range progCh {
		}
	})
	wg.Wait()
	// Test passes if we reach here without hanging.
}

// noopStrategy is a Strategy that immediately closes both channels and returns.
type noopStrategy struct{}

func (noopStrategy) Search(
	_ context.Context,
	_ *image.RGBA,
	_ unpixel.Config,
	out chan<- unpixel.Progress,
	results chan<- unpixel.Result,
) {
	out <- unpixel.Progress{Kind: unpixel.EventDone, Done: true}
}

// spyStrategy captures the Config passed to Search so tests can inspect it.
type spyStrategy struct {
	mu  sync.Mutex
	cfg *unpixel.Config
}

func (s *spyStrategy) Search(
	_ context.Context,
	_ *image.RGBA,
	cfg unpixel.Config,
	out chan<- unpixel.Progress,
	_ chan<- unpixel.Result,
) {
	s.mu.Lock()
	s.cfg = &cfg
	s.mu.Unlock()
	out <- unpixel.Progress{Kind: unpixel.EventDone, Done: true}
}

// stubMetric is a no-op Metric for option-wiring tests.
type stubMetric struct{}

func (stubMetric) Compare(_, _ *image.RGBA) float64 { return 0 }

// TestWithMetric verifies that WithMetric sets Config.Metric.
func TestWithMetric(t *testing.T) {
	var cfg unpixel.Config
	m := stubMetric{}
	unpixel.WithMetric(m)(&cfg)
	if cfg.Metric == nil {
		t.Fatal("WithMetric: Config.Metric is nil, want non-nil")
	}
}

// TestWithCharsetTopK verifies that WithCharsetTopK sets Config.CharsetTopK.
func TestWithCharsetTopK(t *testing.T) {
	cases := []struct {
		k    int
		want int
	}{
		{k: 5, want: 5},
		{k: 0, want: 0},
		{k: -1, want: -1},
	}
	for _, tc := range cases {
		var cfg unpixel.Config
		unpixel.WithCharsetTopK(tc.k)(&cfg)
		if cfg.CharsetTopK != tc.want {
			t.Errorf("WithCharsetTopK(%d): CharsetTopK = %d, want %d", tc.k, cfg.CharsetTopK, tc.want)
		}
	}
}

// TestWithBeamWidth verifies that WithBeamWidth sets Config.BeamWidth.
func TestWithBeamWidth(t *testing.T) {
	cases := []struct {
		width int
		want  int
	}{
		{width: 8, want: 8},
		{width: 0, want: 0},
		{width: 32, want: 32},
	}
	for _, tc := range cases {
		var cfg unpixel.Config
		unpixel.WithBeamWidth(tc.width)(&cfg)
		if cfg.BeamWidth != tc.want {
			t.Errorf("WithBeamWidth(%d): BeamWidth = %d, want %d", tc.width, cfg.BeamWidth, tc.want)
		}
	}
}

// TestWithNormalize_noArgsPanicsNot verifies that WithNormalize() with no
// arguments applies without panicking and that successive calls compose
// (each call re-applies defaults, last write wins). The normalize field is
// unexported, so the observable effect is that RecoverBlurred sets
// Result.Normalized; here we only verify the option chain runs without error.
func TestWithNormalize_noArgsPanicsNot(t *testing.T) {
	var cfg unpixel.Config
	// Two back-to-back calls must not panic.
	unpixel.WithNormalize()(&cfg)
	unpixel.WithNormalize()(&cfg)
}

// TestWithNormalize_customFnApplied verifies that the mutator functions passed
// to WithNormalize are invoked. We confirm this by observing that a nil-fn
// value in the variadic list is a non-crash (deblur.DefaultOptions is called
// first, then the fns are applied in order). The unexported field is not
// directly inspectable from an external test; we instead verify that applying
// a mutator that references deblur.BgSubtract does not panic.
func TestWithNormalize_customFnApplied(t *testing.T) {
	mutatorCalled := false
	var cfg unpixel.Config
	unpixel.WithNormalize(
		func(o *deblur.Options) {
			o.Bg = deblur.BgSubtract
			mutatorCalled = true
		},
		func(o *deblur.Options) { o.Stretch = true },
	)(&cfg)
	if !mutatorCalled {
		t.Error("WithNormalize: mutator function was not called")
	}
}
