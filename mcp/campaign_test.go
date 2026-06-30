//go:build mcpcampaign

// Campaign harness: exercise the MCP decode cores + the opt-in features shipped
// by the #1-#8 program over representative testdata images, to measure whether
// the new features improve recovery and to derive lessons. Observational (never
// fails); run with:
//
//	scripts/gotest-caged.sh go test -tags mcpcampaign -run Campaign -v -timeout 30m ./mcp/
//
// Three experiments:
//
//	A) rerank_weight (#5) on the documented verify_candidates "top-4 not #1" gap.
//	B) expected_format=digits (#6) on the sick digit images.
//	C) font_prior_top_k (#4) on the real corpus (unknown font).
package mcpserver_test

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

func loadImg(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 — test reads committed testdata
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

// TestCampaign_RerankPick — Experiment A: does rerank_weight (#5) push the true
// candidate to Best/Pick where physics alone ranked it top-K-but-not-#1?
func TestCampaign_RerankPick(t *testing.T) {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	cases := []struct {
		file, gt string
		cands    []string
	}{
		{"secret_admin", "admin", []string{"admin", "welcome", "access", "secret"}},
		{"secret_azerty", "azerty", []string{"azerty", "qwerty", "secret", "access"}},
		{"secret_pin1234", "1234", []string{"1234", "1004", "0000", "1111"}},
		{"text_hello", "hello", []string{"hello", "house", "world", "jolly"}},
		{"text_cat", "cat", []string{"cat", "cot", "car", "can"}},
	}
	for _, c := range cases {
		img := loadImg(t, "../testdata/fixtures/"+c.file+".png")
		base, err := mcpserver.VerifyCandidates(t.Context(), img, c.cands, 0, charset, 0)
		if err != nil {
			t.Errorf("%s base: %v", c.file, err)
			continue
		}
		for _, w := range []float64{0.05, 0.1, 0.2} {
			rr, err := mcpserver.VerifyCandidates(t.Context(), img, c.cands, 0, charset, w)
			if err != nil {
				t.Errorf("%s w=%.2f: %v", c.file, w, err)
				continue
			}
			t.Logf("[A rerank] %-15s gt=%-7q  w=0 best=%-8q pick=%-8q | w=%.2f best=%-8q pick=%-8q  %s",
				c.file, c.gt, base.Best, base.Pick, w, rr.Best, rr.Pick, flip(c.gt, base.Best, rr.Best))
		}
	}
}

// flip annotates whether reranking changed Best toward (or away from) the truth.
func flip(gt, baseBest, rrBest string) string {
	switch {
	case baseBest != gt && rrBest == gt:
		return "→ FIXED by rerank"
	case baseBest == gt && rrBest != gt:
		return "→ BROKEN by rerank"
	case baseBest == gt:
		return "(already correct)"
	default:
		return "(still wrong)"
	}
}

// TestCampaign_ExpectedFormatDigits — Experiment B: does expected_format=digits
// (#6) recover (or improve) the sick digit images vs a plain digit charset?
func TestCampaign_ExpectedFormatDigits(t *testing.T) {
	cases := []struct{ file, gt string }{
		{"digits_7d_1234567", "1234567"},
		{"digits_8d_98765432", "98765432"},
		{"digits_9d_012345678", "012345678"},
		{"digits_10d_1029384756", "1029384756"},
	}
	for _, c := range cases {
		img := loadImg(t, "../testdata/sick/"+c.file+".png")
		plain, err := mcpserver.Decode(t.Context(), img, "engine", mcpserver.DecodeOptions{
			CharsetPreset: "digits", MaxLength: len(c.gt),
		})
		if err != nil {
			t.Errorf("%s plain: %v", c.file, err)
			continue
		}
		ef, err := mcpserver.Decode(t.Context(), img, "engine", mcpserver.DecodeOptions{
			CharsetPreset: "digits", ExpectedFormat: "digits", MaxLength: len(c.gt),
		})
		if err != nil {
			t.Errorf("%s ef: %v", c.file, err)
			continue
		}
		t.Logf("[B expfmt] %-22s gt=%q  digits=%q  expfmt=%q  %s",
			c.file, c.gt, plain.Text, ef.Text, hit(c.gt, plain.Text, ef.Text))
	}
}

func hit(gt, a, b string) string {
	switch {
	case b == gt && a != gt:
		return "→ recovered by expected_format"
	case a == gt:
		return "(plain already exact)"
	default:
		return "(both miss)"
	}
}

// TestCampaign_EngineFixtures — Experiment D: the prior MCP campaign capped
// fixtures at 6/17 because it used auto/mosaic; does the engine method WITH the
// right charset_preset (now reachable via MCP) recover the hard fixtures?
func TestCampaign_EngineFixtures(t *testing.T) {
	cases := []struct{ file, gt, charset string }{
		{"alnum_Go2", "Go2", "alnum"},
		{"text_hello", "hello", "lower"},
		{"secret_admin", "admin", "lower"},
		{"secret_azerty", "azerty", "lower"},
		{"secret_pin1234", "1234", "digits"},
	}
	exact := 0
	for _, c := range cases {
		img := loadImg(t, "../testdata/fixtures/"+c.file+".png")
		res, err := mcpserver.Decode(t.Context(), img, "engine", mcpserver.DecodeOptions{
			CharsetPreset: c.charset, MaxLength: len(c.gt),
		})
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		ok := res.Text == c.gt
		if ok {
			exact++
		}
		t.Logf("[D engine] %-15s gt=%-8q charset=%-7s text=%-8q exact=%v", c.file, c.gt, c.charset, res.Text, ok)
	}
	t.Logf("[D engine] hard fixtures recovered via MCP engine: %d/%d", exact, len(cases))
}

// TestCampaign_FontPriorReal — Experiment C: does font_prior_top_k (#4) change
// the engine result on the real corpus (unknown, non-bundled fonts)?
func TestCampaign_FontPriorReal(t *testing.T) {
	cases := []struct{ file, gt string }{
		{"hello-world", "Hello World !"},
		{"marx-clair", "Celui qui ne connaît pas"},
	}
	for _, c := range cases {
		img := loadImg(t, "../testdata/real/"+c.file+".png")
		base, err := mcpserver.Decode(t.Context(), img, "engine", mcpserver.DecodeOptions{CharsetPreset: "ascii"})
		if err != nil {
			t.Errorf("%s base: %v", c.file, err)
			continue
		}
		fp, err := mcpserver.Decode(t.Context(), img, "engine", mcpserver.DecodeOptions{CharsetPreset: "ascii", FontPriorTopK: 3})
		if err != nil {
			t.Errorf("%s fp: %v", c.file, err)
			continue
		}
		t.Logf("[C fontprior] %-12s gt=%q  base=%q (font %q)  top3=%q (font %q)",
			c.file, c.gt, base.Text, base.Font, fp.Text, fp.Font)
	}
}
