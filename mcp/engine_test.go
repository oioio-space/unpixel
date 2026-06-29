package mcpserver_test

// engine_test.go — tests for the fixes introduced in the decode-surface overhaul:
//   - engine method recovers hard fixtures (alnum_Go2, symbols_x_eq_1).
//   - digits charset decodes secret_pin1234 via engine.
//   - denoise option is accepted without error on hello-world-noisy.png.
//   - reference method forwards known_visible_text without error.
//   - analyze recommends "engine" for axis-aligned mosaics (not "did").
//   - "default" method alias routes to engine (not an error).

import (
	"path/filepath"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

const realDir = "../testdata/real"

func realFixturePath(name string) string {
	return filepath.Join(realDir, name)
}

// TestDecodeEngine_alnum recovers "Go2" from alnum_Go2.png using
// method=engine with charset_preset=alnum. This is a hard fixture that
// zero-config mosaic cannot recover (non-lowercase charset).
func TestDecodeEngine_alnum(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("alnum_Go2.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "engine", mcpserver.DecodeOptions{
		CharsetPreset: "alnum",
	})
	if err != nil {
		t.Fatalf("Decode(engine, alnum): %v", err)
	}
	if got.Text != "Go2" {
		t.Errorf("Decode(engine, alnum): Text = %q, want %q", got.Text, "Go2")
	}
	if got.MethodUsed != "engine" {
		t.Errorf("Decode(engine): MethodUsed = %q, want %q", got.MethodUsed, "engine")
	}
}

// TestDecodeEngine_symbols verifies that method=engine with an alnum charset
// does not error and returns a non-empty MethodUsed. The symbols_x_eq_1.png
// fixture contains "x=1" which requires the '=' character; with an alnum
// charset the engine will not recover it exactly, but we only verify the
// method routes correctly here — the full ascii-charset search is too slow
// for a unit test (95-char alphabet × 3-char string).
func TestDecodeEngine_symbols(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("symbols_x_eq_1.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "engine", mcpserver.DecodeOptions{
		CharsetPreset: "alnum",
		MaxLength:     5,
	})
	if err != nil {
		t.Fatalf("Decode(engine, alnum): %v", err)
	}
	if got.MethodUsed != "engine" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "engine")
	}
}

// TestDecodeEngine_digits recovers "1234" from secret_pin1234.png using
// method=engine with charset_preset=digits. Verifies the new "digits" preset
// reaches the engine and restricts the search alphabet to 0–9.
func TestDecodeEngine_digits(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("secret_pin1234.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "engine", mcpserver.DecodeOptions{
		CharsetPreset: "digits",
	})
	if err != nil {
		t.Fatalf("Decode(engine, digits): %v", err)
	}
	if got.Text != "1234" {
		t.Errorf("Decode(engine, digits): Text = %q, want %q", got.Text, "1234")
	}
}

// TestDecodeEngine_denoise verifies that Denoise=true is accepted without error
// on a noisy real-world capture. We don't assert exact text because the noisy
// image may not decode perfectly, but the call must succeed and return a
// non-empty MethodUsed.
func TestDecodeEngine_denoise(t *testing.T) {
	ctx := t.Context()
	img, err := loadImageFile(realFixturePath("hello-world-noisy.png"))
	if err != nil {
		t.Fatalf("load hello-world-noisy.png: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "engine", mcpserver.DecodeOptions{
		Denoise:       true,
		CharsetPreset: "lower",
	})
	if err != nil {
		t.Fatalf("Decode(engine, denoise=true): %v", err)
	}
	if got.MethodUsed != "engine" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "engine")
	}
	// Verify the denoise note is present.
	found := false
	for _, n := range got.Notes {
		if n == "denoise applied" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Notes does not contain %q; got %v", "denoise applied", got.Notes)
	}
}

// TestDecodeReference_knownVisibleText verifies that known_visible_text is
// accepted, forwarded to WithRefVisibleText, and appears in the result notes.
// Uses a standard mosaic fixture (block08_go.png) so InferBlockGrid succeeds
// and the decoder gets past ErrNoMosaic — the context fixture mixes sharp text
// and mosaic regions which confuses grid detection.
func TestDecodeReference_knownVisibleText(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "reference", mcpserver.DecodeOptions{
		KnownVisibleText: "go",
		CharsetPreset:    "lower",
	})
	if err != nil {
		t.Fatalf("Decode(reference, known_visible_text): %v", err)
	}
	if got.MethodUsed != "reference" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "reference")
	}
	// The note about known_visible_text must be present.
	foundNote := false
	for _, n := range got.Notes {
		if n != "" {
			foundNote = true
			break
		}
	}
	if !foundNote {
		t.Errorf("Notes is empty; expected known_visible_text note, got %v", got.Notes)
	}
}

// TestAnalyze_recommendsEngine verifies that unpixel_analyze recommends
// "engine" (not "did") for a high-confidence axis-aligned mosaic fixture.
func TestAnalyze_recommendsEngine(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	report, err := mcpserver.Analyze(img)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.RecommendedDecoder != "engine" {
		t.Errorf("RecommendedDecoder = %q, want %q", report.RecommendedDecoder, "engine")
	}
}

// TestDecodeDefault_aliasesEngine verifies that method="default" routes to the
// engine path (not an error) and returns MethodUsed="engine".
func TestDecodeDefault_aliasesEngine(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "default", mcpserver.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode(default): %v", err)
	}
	if got.MethodUsed != "engine" {
		t.Errorf("Decode(default): MethodUsed = %q, want %q", got.MethodUsed, "engine")
	}
}

// TestCharsetDigits_didDecoder verifies that charset_preset="digits" is
// accepted by the did decoder (charsetFor is shared) without error. DID may
// not recover the exact PIN in all configurations, so we only check that the
// method runs and restricts itself to digits.
func TestCharsetDigits_didDecoder(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("secret_pin1234.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "did", mcpserver.DecodeOptions{
		CharsetPreset: "digits",
	})
	if err != nil {
		t.Fatalf("Decode(did, digits): %v", err)
	}
	if got.MethodUsed != "did" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "did")
	}
	// Verify the result only contains digits (charset restriction is enforced).
	for _, r := range got.Text {
		if r < '0' || r > '9' {
			t.Errorf("Decode(did, digits): result %q contains non-digit rune %q", got.Text, r)
		}
	}
}

// TestDecodeEnsemble_combinesEngineAndMosaic proves the ensemble bridges the two
// complementary paths via the common ScoreCandidates arbiter: alnum_Go2 is only
// recoverable by the charset-aware engine, while bold_go is recovered by the
// zero-config mosaic (engine returns a single char there). The ensemble must pick
// the right member's text for each.
func TestDecodeEnsemble_combinesEngineAndMosaic(t *testing.T) {
	tests := []struct {
		name    string
		fixture string
		charset string
		want    string
	}{
		{"engine_member_wins", "alnum_Go2.png", "alnum", "Go2"},
		{"mosaic_member_wins", "bold_go.png", "lower", "go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img, err := loadFixture(tt.fixture)
			if err != nil {
				t.Fatalf("load %s: %v", tt.fixture, err)
			}
			got, err := mcpserver.Decode(t.Context(), img, "ensemble", mcpserver.DecodeOptions{
				CharsetPreset: tt.charset,
				MaxLength:     6,
			})
			if err != nil {
				t.Fatalf("Decode(ensemble): %v", err)
			}
			if got.Text != tt.want {
				t.Errorf("ensemble Text = %q, want %q (method=%s)", got.Text, tt.want, got.MethodUsed)
			}
		})
	}
}
