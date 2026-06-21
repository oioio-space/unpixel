package unpixel_test

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/oioio-space/unpixel"
	"github.com/oioio-space/unpixel/internal/fixture/blurfixture"
)

// TestRecoverBlurred_matrix loads each committed blur fixture from
// testdata/blur and calls RecoverBlurred — without supplying σ — asserting
// that the text is recovered. It is the primary quality guard for the
// zero-config σ-search capability.
//
// The matrix covers 12 of the 14 fixtures (go×4σ, cat×4σ, hello×4σ) and
// always runs in a few seconds. The two "connect" fixtures (7-char search
// space) are excluded from this matrix: they require a language model prior
// (P7.3b) to converge reliably and are tested separately in
// TestRecoverBlurred_longerWord_connect (blur_prior_test.go, -short gated).
//
// Under -short the heavier single-word subsets (hello and cat at σ=6) are
// also skipped so the always-on subset (go + cat at σ=2/3/4) stays under 5s.
func TestRecoverBlurred_matrix(t *testing.T) {
	specs := loadBlurManifest(t)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

	t.Log("text\tσ_true\tσ_chosen\trecovered\tbest_guess")

	for _, s := range specs {
		s := s
		t.Run(s.Name, func(t *testing.T) {
			// "connect" (7-char) needs a language model to converge reliably —
			// excluded from the blind matrix; see TestRecoverBlurred_longerWord_connect.
			if s.Text == "connect" {
				t.Skip("7-char blind search too slow without language prior (see blur_prior_test.go)")
			}
			// Under -short skip the heavier cases so the always-on subset stays fast.
			if testing.Short() && (s.Text == "hello" || (s.Text == "cat" && s.Sigma == 6)) {
				t.Skip("skipped under -short")
			}

			path := filepath.Join("testdata", "blur", s.File)
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("open fixture %s: %v", path, err)
			}
			img, err := png.Decode(f)
			_ = f.Close()
			if err != nil {
				t.Fatalf("decode fixture %s: %v", path, err)
			}

			res, err := unpixel.RecoverBlurred(
				t.Context(), img,
				unpixel.WithCharset(s.Charset),
				unpixel.WithMaxLength(len(s.Text)+1),
				unpixel.WithStyle(style),
			)
			if err != nil {
				t.Fatalf("RecoverBlurred: %v", err)
			}

			ok := recoveredText(res, s.Text)
			t.Logf("%s\t%.0f\t%.2f\t%v\t%q",
				s.Text, s.Sigma, res.BlurSigma, ok, res.BestGuess)

			if !ok {
				t.Errorf("σ=%.0f text=%q: missed; best=%q (BlurSigma=%.2f, BestTotal=%.4f)",
					s.Sigma, s.Text, res.BestGuess, res.BlurSigma, res.BestTotal)
			}
		})
	}
}

// TestRecoverBlurred_sigmaEstimate checks that the chosen BlurSigma is
// within ±1.5 of the true sigma (a coarse but meaningful accuracy gate).
// It runs on the always-on subset only.
func TestRecoverBlurred_sigmaEstimate(t *testing.T) {
	specs := loadBlurManifest(t)
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

	for _, s := range specs {
		if s.Text != "go" {
			continue // fast subset: just "go" across all sigmas
		}
		s := s
		t.Run(fmt.Sprintf("σ_accuracy_%.0f", s.Sigma), func(t *testing.T) {
			path := filepath.Join("testdata", "blur", s.File)
			f, err := os.Open(path)
			if err != nil {
				t.Fatalf("open fixture %s: %v", path, err)
			}
			img, err := png.Decode(f)
			_ = f.Close()
			if err != nil {
				t.Fatalf("decode fixture %s: %v", path, err)
			}

			res, err := unpixel.RecoverBlurred(
				t.Context(), img,
				unpixel.WithCharset(s.Charset),
				unpixel.WithMaxLength(len(s.Text)+1),
				unpixel.WithStyle(style),
			)
			if err != nil {
				t.Fatalf("RecoverBlurred: %v", err)
			}

			diff := res.BlurSigma - s.Sigma
			if diff < 0 {
				diff = -diff
			}
			t.Logf("true σ=%.0f chosen σ=%.2f diff=%.2f", s.Sigma, res.BlurSigma, diff)
			if diff > 1.5 {
				t.Errorf("σ estimate off by %.2f (true=%.0f, chosen=%.2f)",
					diff, s.Sigma, res.BlurSigma)
			}
		})
	}
}

// BenchmarkRecoverBlurred measures the full σ-search cost (≈ N_candidates ×
// single Recover) on a representative blur_go_s3 fixture. Setup is outside
// b.Loop so only RecoverBlurred is timed.
func BenchmarkRecoverBlurred(b *testing.B) {
	path := filepath.Join("testdata", "blur", "blur_go_s3.png")
	f, err := os.Open(path)
	if err != nil {
		b.Fatalf("open fixture: %v", err)
	}
	img, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		b.Fatalf("decode fixture: %v", err)
	}
	style := unpixel.Style{FontSize: 32, PaddingTop: 8, PaddingLeft: 8}

	b.ReportAllocs()
	for b.Loop() {
		res, err := unpixel.RecoverBlurred(
			b.Context(), img,
			unpixel.WithCharset("go abcde"),
			unpixel.WithMaxLength(3),
			unpixel.WithStyle(style),
		)
		if err != nil {
			b.Fatal(err)
		}
		blurSink = res
	}
}

// blurSink is a package-level sink to defeat dead-code elimination in benchmarks.
var blurSink unpixel.Result

// loadBlurManifest reads testdata/blur/manifest.json and returns the specs.
func loadBlurManifest(t *testing.T) []blurfixture.Spec {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "blur", "manifest.json"))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer func() { _ = f.Close() }()
	var specs []blurfixture.Spec
	if err := json.NewDecoder(f).Decode(&specs); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return specs
}
