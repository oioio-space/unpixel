package mosaictext_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/oioio-space/unpixel/mosaictext"
)

// syntheticDecoder returns a fixed Result with the given text and distance,
// simulating a decoder that produces a specific output. The errDecoder variant
// returns an error instead.
func syntheticDecoder(text string, dist float64) mosaictext.EnsembleDecoder {
	return func(_ context.Context, _ image.Image) (mosaictext.Result, error) {
		return mosaictext.Result{Text: text, Distance: dist}, nil
	}
}

func errDecoder(err error) mosaictext.EnsembleDecoder {
	return func(_ context.Context, _ image.Image) (mosaictext.Result, error) {
		return mosaictext.Result{}, err
	}
}

func emptyDecoder() mosaictext.EnsembleDecoder {
	return func(_ context.Context, _ image.Image) (mosaictext.Result, error) {
		return mosaictext.Result{}, nil // empty Text — should be skipped
	}
}

// smallWhiteImage is a 1×1 white image used as a placeholder input for
// synthetic-decoder tests (the decoders ignore it).
func smallWhiteImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.SetRGBA(0, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	return img
}

// TestDecodeEnsemble_PicksBestDistance verifies that the ensemble selects the
// result with the lowest distance when two decoders return different texts.
func TestDecodeEnsemble_PicksBestDistance(t *testing.T) {
	ctx := t.Context()
	img := smallWhiteImage()

	decoders := []mosaictext.EnsembleDecoder{
		syntheticDecoder("worse", 50.0),
		syntheticDecoder("better", 10.0),
	}

	got, err := mosaictext.DecodeEnsemble(ctx, img, decoders)
	if err != nil {
		t.Fatalf("DecodeEnsemble: %v", err)
	}
	if got.Text != "better" {
		t.Errorf("DecodeEnsemble selected %q (dist=%.1f), want %q (dist=10.0)",
			got.Text, got.Distance, "better")
	}
	if got.Distance != 10.0 {
		t.Errorf("DecodeEnsemble distance = %.1f, want 10.0", got.Distance)
	}
}

// TestDecodeEnsemble_NeverWorseThanBest verifies that the ensemble result
// distance is always ≤ the minimum distance among all individual decoders.
func TestDecodeEnsemble_NeverWorseThanBest(t *testing.T) {
	ctx := t.Context()
	img := smallWhiteImage()

	cases := []struct {
		name     string
		decoders []mosaictext.EnsembleDecoder
		wantText string
		wantDist float64
	}{
		{
			name: "A wins",
			decoders: []mosaictext.EnsembleDecoder{
				syntheticDecoder("A", 5.0),
				syntheticDecoder("B", 20.0),
			},
			wantText: "A",
			wantDist: 5.0,
		},
		{
			name: "B wins",
			decoders: []mosaictext.EnsembleDecoder{
				syntheticDecoder("A", 30.0),
				syntheticDecoder("B", 8.0),
			},
			wantText: "B",
			wantDist: 8.0,
		},
		{
			name: "C wins in three-way",
			decoders: []mosaictext.EnsembleDecoder{
				syntheticDecoder("A", 25.0),
				syntheticDecoder("B", 15.0),
				syntheticDecoder("C", 3.0),
			},
			wantText: "C",
			wantDist: 3.0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mosaictext.DecodeEnsemble(ctx, img, tc.decoders)
			if err != nil {
				t.Fatalf("DecodeEnsemble: %v", err)
			}
			if got.Text != tc.wantText {
				t.Errorf("selected %q (dist=%.1f), want %q (dist=%.1f)",
					got.Text, got.Distance, tc.wantText, tc.wantDist)
			}
			if got.Distance > tc.wantDist {
				t.Errorf("ensemble distance %.1f > best individual %.1f — safety property violated",
					got.Distance, tc.wantDist)
			}
		})
	}
}

// TestDecodeEnsemble_SkipsErrors verifies that a decoder that errors is skipped
// and the ensemble returns the result from the surviving decoder.
func TestDecodeEnsemble_SkipsErrors(t *testing.T) {
	ctx := t.Context()
	img := smallWhiteImage()

	sentinel := errors.New("decoder failed")
	decoders := []mosaictext.EnsembleDecoder{
		errDecoder(sentinel),
		syntheticDecoder("survivor", 12.0),
	}

	got, err := mosaictext.DecodeEnsemble(ctx, img, decoders)
	if err != nil {
		t.Fatalf("DecodeEnsemble: %v", err)
	}
	if got.Text != "survivor" {
		t.Errorf("DecodeEnsemble = %q, want %q", got.Text, "survivor")
	}
}

// TestDecodeEnsemble_SkipsEmpty verifies that a decoder returning an empty Text
// is skipped.
func TestDecodeEnsemble_SkipsEmpty(t *testing.T) {
	ctx := t.Context()
	img := smallWhiteImage()

	decoders := []mosaictext.EnsembleDecoder{
		emptyDecoder(),
		syntheticDecoder("hello", 7.0),
	}

	got, err := mosaictext.DecodeEnsemble(ctx, img, decoders)
	if err != nil {
		t.Fatalf("DecodeEnsemble: %v", err)
	}
	if got.Text != "hello" {
		t.Errorf("DecodeEnsemble = %q, want %q", got.Text, "hello")
	}
}

// TestDecodeEnsemble_AllErrors verifies that ErrNoContent is returned when all
// decoders fail or produce empty results.
func TestDecodeEnsemble_AllErrors(t *testing.T) {
	ctx := t.Context()
	img := smallWhiteImage()

	decoders := []mosaictext.EnsembleDecoder{
		errDecoder(mosaictext.ErrNoMosaic),
		errDecoder(mosaictext.ErrNoContent),
		emptyDecoder(),
	}

	_, err := mosaictext.DecodeEnsemble(ctx, img, decoders)
	if !errors.Is(err, mosaictext.ErrNoContent) {
		t.Errorf("DecodeEnsemble(all-fail) = %v, want ErrNoContent", err)
	}
}

// TestDecodeEnsemble_EmptySet verifies that an empty decoder set returns ErrNoContent.
func TestDecodeEnsemble_EmptySet(t *testing.T) {
	ctx := t.Context()
	img := smallWhiteImage()

	_, err := mosaictext.DecodeEnsemble(ctx, img, nil)
	if !errors.Is(err, mosaictext.ErrNoContent) {
		t.Errorf("DecodeEnsemble(nil) = %v, want ErrNoContent", err)
	}
}

// TestDecodeEnsemble_TieBreak verifies that ties are broken deterministically
// by decoder order (first decoder wins on equal distance).
func TestDecodeEnsemble_TieBreak(t *testing.T) {
	ctx := t.Context()
	img := smallWhiteImage()

	decoders := []mosaictext.EnsembleDecoder{
		syntheticDecoder("first", 10.0),
		syntheticDecoder("second", 10.0),
	}

	got, err := mosaictext.DecodeEnsemble(ctx, img, decoders)
	if err != nil {
		t.Fatalf("DecodeEnsemble: %v", err)
	}
	// Ties broken by decoder order: first decoder wins.
	if got.Text != "first" {
		t.Errorf("tie-break: got %q, want %q (first by order)", got.Text, "first")
	}
}

// TestDecodeEnsemble_ContextCancel verifies that a cancelled context propagates
// and DecodeEnsemble returns a context error.
func TestDecodeEnsemble_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	img := smallWhiteImage()
	blockingDecoder := mosaictext.EnsembleDecoder(func(ctx context.Context, _ image.Image) (mosaictext.Result, error) {
		return mosaictext.Result{}, ctx.Err()
	})

	_, err := mosaictext.DecodeEnsemble(ctx, img, []mosaictext.EnsembleDecoder{blockingDecoder})
	if err == nil {
		t.Error("expected error on cancelled context, got nil")
	}
}
