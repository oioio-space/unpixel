package defaults_test

import (
	"image"
	"sync/atomic"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/internal/secrets"
)

// renderPixelated renders text with the default components and pixelates it,
// returning an in-memory mosaic image. No file or network I/O.
func renderPixelated(t *testing.T, text string, block int, fontSize float64) image.Image {
	t.Helper()
	style := unpixel.Style{FontSize: fontSize}
	cfg := unpixel.Config{BlockSize: block, Style: style}
	if err := defaults.Wire(&cfg); err != nil {
		t.Fatalf("wire defaults: %v", err)
	}
	rendered, _, err := cfg.Renderer.Render(text, style)
	if err != nil {
		t.Fatalf("render %q: %v", text, err)
	}
	return cfg.Pixelator.Pixelate(rendered, 0, 0)
}

// countingMetric wraps a Metric and counts comparisons atomically.
type countingMetric struct {
	inner unpixel.Metric
	count *int64
}

func (m countingMetric) Compare(a, b *image.RGBA) float64 {
	atomic.AddInt64(m.count, 1)
	return m.inner.Compare(a, b)
}

// TestExpectedFormat_digitsRecovers verifies that a digit string pixelated
// in memory is recovered by the engine when WithExpectedFormat(FormatDigits)
// is applied.
func TestExpectedFormat_digitsRecovers(t *testing.T) {
	const secret = "8675309"
	const block = 6
	img := renderPixelated(t, secret, block, 24)

	res, err := unpixel.Recover(t.Context(), img,
		unpixel.WithCharset("0123456789"),
		unpixel.WithBlockSize(block),
		unpixel.WithStyle(unpixel.Style{FontSize: 24}),
		unpixel.WithMaxLength(len(secret)),
		unpixel.WithExpectedFormat(secrets.FormatDigits),
	)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if res.BestGuess != secret {
		t.Errorf("digits recovery = %q; want %q", res.BestGuess, secret)
	}
}

// TestExpectedFormat_creditCardPassesLuhn verifies that a pixelated credit
// card number is recovered as a Luhn-valid card when WithExpectedFormat
// (FormatCreditCard) is applied.
func TestExpectedFormat_creditCardPassesLuhn(t *testing.T) {
	const card = "4532015112830366" // valid Luhn-16
	const block = 6
	img := renderPixelated(t, card, block, 24)

	res, err := unpixel.Recover(t.Context(), img,
		unpixel.WithCharset("0123456789"),
		unpixel.WithBlockSize(block),
		unpixel.WithStyle(unpixel.Style{FontSize: 24}),
		unpixel.WithMaxLength(len(card)),
		unpixel.WithExpectedFormat(secrets.FormatCreditCard),
	)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if !secrets.Valid(secrets.FormatCreditCard, res.BestGuess) {
		t.Errorf("card recovery %q does not pass Luhn/length", res.BestGuess)
	}
}

// TestExpectedFormat_fewerNodesThanUnconstrained verifies that a constrained
// search (FormatDigits via CharsetAlnum) evaluates fewer metric comparisons
// than an unconstrained search over the same broad alphabet, while both runs
// still recover the secret.
func TestExpectedFormat_fewerNodesThanUnconstrained(t *testing.T) {
	const secret = "8675309"
	const block = 6

	// Use CharsetAlnum for both runs so the format constraint has room to prune:
	// WithExpectedFormat(FormatDigits) intersects CharsetAlnum with the 10 digit
	// runes at every position, reducing the branching factor from 63 to 10.
	count := func(format secrets.Format) (int64, string) {
		img := renderPixelated(t, secret, block, 24)
		var n int64
		base := unpixel.Config{}
		if err := defaults.Wire(&base); err != nil {
			t.Fatalf("wire: %v", err)
		}
		opts := []unpixel.Option{
			unpixel.WithCharset(unpixel.CharsetAlnum),
			unpixel.WithBlockSize(block),
			unpixel.WithStyle(unpixel.Style{FontSize: 24}),
			unpixel.WithMaxLength(len(secret)),
			unpixel.WithMetric(countingMetric{inner: base.Metric, count: &n}),
		}
		if format != secrets.FormatNone {
			opts = append(opts, unpixel.WithExpectedFormat(format))
		}
		res, err := unpixel.Recover(t.Context(), img, opts...)
		if err != nil {
			t.Fatalf("recover: %v", err)
		}
		return n, res.BestGuess
	}

	unconstrained, ucGuess := count(secrets.FormatNone)
	constrained, cGuess := count(secrets.FormatDigits)
	if constrained >= unconstrained {
		t.Errorf("constrained evals = %d; want fewer than unconstrained %d", constrained, unconstrained)
	}
	// Both runs must succeed: the point is pruning, not degraded recall.
	if cGuess != secret {
		t.Errorf("constrained recovery = %q; want %q", cGuess, secret)
	}
	if ucGuess != secret {
		t.Errorf("unconstrained recovery = %q; want %q", ucGuess, secret)
	}
}

// TestExpectedFormat_noFormatByteIdentical verifies that FormatNone produces
// a byte-identical result to not setting the format option at all.
func TestExpectedFormat_noFormatByteIdentical(t *testing.T) {
	const secret = "go2"
	const block = 6
	img := renderPixelated(t, secret, block, 24)

	opts := []unpixel.Option{
		unpixel.WithCharset(unpixel.CharsetAlnum),
		unpixel.WithBlockSize(block),
		unpixel.WithStyle(unpixel.Style{FontSize: 24}),
		unpixel.WithMaxLength(len(secret)),
	}
	a, err := unpixel.Recover(t.Context(), img, opts...)
	if err != nil {
		t.Fatalf("recover a: %v", err)
	}
	b, err := unpixel.Recover(t.Context(), img, append(opts, unpixel.WithExpectedFormat(secrets.FormatNone))...)
	if err != nil {
		t.Fatalf("recover b: %v", err)
	}
	if a.BestGuess != b.BestGuess {
		t.Errorf("FormatNone changed result: %q vs %q", a.BestGuess, b.BestGuess)
	}
}
