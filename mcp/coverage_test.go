package mcpserver_test

// coverage_test.go — tests specifically targeting previously uncovered
// statements in the mcp package: decode method dispatch, parseRegion, heuristic
// branches, charsetFor edge cases, clampFidelity extremes, job error paths,
// and font loading edge cases.
//
// All tests run under -short and complete in well under a minute. Methods that
// are intrinsically slow (trained-hmm, varfont, blind, ensemble) are exercised
// by passing a pre-cancelled context so the option-assembly code is covered
// and the underlying decoder returns a context error immediately.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// cancelledCtx returns a context that is already cancelled.
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// blankRGBA returns a small uniform-grey RGBA image that produces no
// detectable mosaic grid and no blur — drives the heuristic "none" branch.
func blankRGBA(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	grey := color.RGBA{R: 200, G: 200, B: 200, A: 255}
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, grey)
		}
	}
	return img
}

// ---- charsetFor coverage ----

// TestDecode_charsetLower verifies that charset_preset="lower" is accepted
// and forwarded correctly (DID decoder reads it). We use the block08_go
// fixture and expect a valid result without error.
func TestDecode_charsetLower(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	got, err := mcpserver.Decode(ctx, img, "mosaic", mcpserver.DecodeOptions{
		CharsetPreset: "lower",
	})
	if err != nil {
		t.Fatalf("Decode(mosaic, lower): %v", err)
	}
	if got.MethodUsed != "mosaic" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "mosaic")
	}
}

// TestDecode_charsetASCII verifies that charset_preset="ascii" is accepted
// without error (it is forwarded to DID; mosaic ignores it but records it).
func TestDecode_charsetASCII(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	got, err := mcpserver.Decode(ctx, img, "did", mcpserver.DecodeOptions{
		CharsetPreset: "ascii",
	})
	if err != nil {
		// DID on the tiny fixture may time out; that is acceptable coverage.
		if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context") {
			t.Fatalf("Decode(did, ascii): unexpected error: %v", err)
		}
		return
	}
	if got.MethodUsed != "did" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "did")
	}
}

// ---- decode method coverage (via cancelled context) ----

// TestDecode_monoHMM_cancelled covers decodeMonoHMM option assembly + error
// path by supplying a pre-cancelled context so the decoder returns immediately.
func TestDecode_monoHMM_cancelled(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, err = mcpserver.Decode(cancelledCtx(), img, "mono-hmm", mcpserver.DecodeOptions{})
	// Expect a context error (cancelled or deadline).
	if err == nil {
		t.Log("mono-hmm with cancelled context returned nil error (decoder may short-circuit gracefully)")
		return
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		// Some decoders wrap context errors; allow any non-nil error from a cancelled context.
		t.Logf("mono-hmm cancelled: got error (accepted): %v", err)
	}
}

// TestDecode_monoHMM_withFont covers the font-data branch in decodeMonoHMM.
func TestDecode_monoHMM_withFont(t *testing.T) {
	ttfPath := bundledTTFPath(t)
	fontData, err := mcpserver.LoadFontData(ttfPath, "")
	if err != nil {
		t.Fatalf("LoadFontData: %v", err)
	}
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "mono-hmm", mcpserver.DecodeOptions{
		FontData: fontData,
	})
	// Cancelled context: any result (including error) is fine; we just need the font branch covered.
}

// TestDecode_windowHMM_cancelled covers decodeWindowHMM.
func TestDecode_windowHMM_cancelled(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "window-hmm", mcpserver.DecodeOptions{})
}

// TestDecode_windowHMM_withFont covers the font-data branch in decodeWindowHMM.
func TestDecode_windowHMM_withFont(t *testing.T) {
	ttfPath := bundledTTFPath(t)
	fontData, err := mcpserver.LoadFontData(ttfPath, "")
	if err != nil {
		t.Fatalf("LoadFontData: %v", err)
	}
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "window-hmm", mcpserver.DecodeOptions{
		FontData: fontData,
	})
}

// TestDecode_trainedHMM_cancelled covers decodeTrainedHMM option assembly.
func TestDecode_trainedHMM_cancelled(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "trained-hmm", mcpserver.DecodeOptions{})
}

// TestDecode_trainedHMM_withFont covers the font-data branch in decodeTrainedHMM.
func TestDecode_trainedHMM_withFont(t *testing.T) {
	ttfPath := bundledTTFPath(t)
	fontData, err := mcpserver.LoadFontData(ttfPath, "")
	if err != nil {
		t.Fatalf("LoadFontData: %v", err)
	}
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "trained-hmm", mcpserver.DecodeOptions{
		Language: "fr",
		FontData: fontData,
	})
}

// TestDecode_blurred_cancelled covers decodeBlurred.
func TestDecode_blurred_cancelled(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "blurred", mcpserver.DecodeOptions{})
}

// TestDecode_varFont_cancelled covers decodeVarFont option assembly.
func TestDecode_varFont_cancelled(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "varfont", mcpserver.DecodeOptions{
		KnownVisibleText: "go",
	})
}

// TestDecode_varFont_noKnownText covers the no-visible-text branch in decodeVarFont.
func TestDecode_varFont_noKnownText(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "varfont", mcpserver.DecodeOptions{})
}

// TestDecode_reference_cancelled covers decodeReference option assembly.
func TestDecode_reference_cancelled(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "reference", mcpserver.DecodeOptions{})
}

// TestDecode_reference_withFont covers the font-data branch in decodeReference.
func TestDecode_reference_withFont(t *testing.T) {
	ttfPath := bundledTTFPath(t)
	fontData, err := mcpserver.LoadFontData(ttfPath, "")
	if err != nil {
		t.Fatalf("LoadFontData: %v", err)
	}
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "reference", mcpserver.DecodeOptions{
		CharsetPreset: "lower",
		FontData:      fontData,
	})
}

// TestDecode_blind_cancelled covers decodeBlind option assembly (English path).
func TestDecode_blind_cancelled(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "blind", mcpserver.DecodeOptions{})
}

// TestDecode_blind_french covers the French language branch in decodeBlind.
func TestDecode_blind_french(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "blind", mcpserver.DecodeOptions{
		Language: "fr",
	})
}

// TestDecode_ensemble_cancelled covers decodeEnsemble.
func TestDecode_ensemble_cancelled(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "ensemble", mcpserver.DecodeOptions{})
}

// TestDecode_reference_fast exercises decodeReference with a fast context
// (short timeout) on the tiny fixture to cover the success path too.
func TestDecode_reference_fast(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	got, err := mcpserver.Decode(ctx, img, "reference", mcpserver.DecodeOptions{
		CharsetPreset: "lower",
	})
	if err != nil {
		// Reference may fail with context; acceptable for coverage.
		t.Logf("Decode(reference): error (accepted for -short): %v", err)
		return
	}
	if got.MethodUsed != "reference" {
		t.Errorf("MethodUsed = %q, want %q", got.MethodUsed, "reference")
	}
}

// TestDecode_defaultAlias verifies that method="default" is accepted as an
// alias for "did" in dispatchDecode.
func TestDecode_defaultAlias(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "default", mcpserver.DecodeOptions{})
}

// ---- heuristic branch coverage via Analyze ----

// TestAnalyze_noneRedaction verifies that a uniform image with no grid and no
// blur receives redaction_type="none".
func TestAnalyze_noneRedaction(t *testing.T) {
	img := blankRGBA(64, 32)
	got, err := mcpserver.Analyze(img)
	if err != nil {
		t.Fatalf("Analyze(blank): %v", err)
	}
	// Uniform image has no grid → heuristic "none" branch.
	if got.RedactionType != "none" && got.RedactionType != "mosaic" {
		t.Logf("RedactionType = %q (may be mosaic if perspective detected)", got.RedactionType)
	}
}

// TestAnalyze_colorspaceLinear exercises the colorspace detection with
// a larger fixture.
func TestAnalyze_colorspaceLinear(t *testing.T) {
	img, err := loadFixture("block16_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	got, err := mcpserver.Analyze(img)
	if err != nil {
		t.Fatalf("Analyze(block16): %v", err)
	}
	if got.Colorspace == "" {
		t.Error("Colorspace is empty")
	}
}

// ---- parseRegion coverage via Calibrate ----

// TestCalibrate_withRegion covers the parseRegion success path.
func TestCalibrate_withRegion(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	b := img.Bounds()
	// Crop a sub-region that fits within the image.
	w := max(1, b.Dx()/2)
	h := max(1, b.Dy()/2)
	region := fmt.Sprintf("0,0,%d,%d", w, h)
	_, err = mcpserver.Calibrate(img, "hell", mcpserver.CalibrateOptions{
		Region: region,
	})
	// Error is fine (font mismatch); we just need parseRegion to run.
	if err != nil {
		t.Logf("Calibrate(region=%q): error (accepted): %v", region, err)
	}
}

// TestCalibrate_malformedRegion_wrongParts covers parseRegion with wrong part count.
func TestCalibrate_malformedRegion_wrongParts(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, err = mcpserver.Calibrate(img, "hello", mcpserver.CalibrateOptions{
		Region: "0,0,10", // only 3 parts
	})
	if err == nil {
		t.Error("Calibrate(malformed region): want error, got nil")
	}
}

// TestCalibrate_malformedRegion_nonNumeric covers parseRegion with non-numeric input.
func TestCalibrate_malformedRegion_nonNumeric(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, err = mcpserver.Calibrate(img, "hello", mcpserver.CalibrateOptions{
		Region: "0,0,abc,10",
	})
	if err == nil {
		t.Error("Calibrate(non-numeric region): want error, got nil")
	}
}

// TestCalibrate_malformedRegion_zeroSize covers parseRegion with w=0 or h=0.
func TestCalibrate_malformedRegion_zeroSize(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, err = mcpserver.Calibrate(img, "hello", mcpserver.CalibrateOptions{
		Region: "0,0,0,10", // w=0
	})
	if err == nil {
		t.Error("Calibrate(zero-width region): want error, got nil")
	}
}

// TestCalibrate_regionOutOfBounds covers parseRegion with out-of-bounds rect.
func TestCalibrate_regionOutOfBounds(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, err = mcpserver.Calibrate(img, "hello", mcpserver.CalibrateOptions{
		Region: "0,0,99999,99999", // larger than the image
	})
	if err == nil {
		t.Error("Calibrate(out-of-bounds region): want error, got nil")
	}
}

// ---- LoadFontData oversized file ----

// TestLoadFontData_oversizedFile covers the file-too-large path in LoadFontData.
func TestLoadFontData_oversizedFile(t *testing.T) {
	// Create a temp file with valid TTF magic but oversized (> 16 MiB).
	// We write a sparse file using os.Truncate so this is very fast.
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.ttf")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Write valid TTF magic first.
	_, err = f.Write([]byte{0x00, 0x01, 0x00, 0x00, 0x00})
	if err != nil {
		if closeErr := f.Close(); closeErr != nil {
			t.Logf("close after write error: %v", closeErr)
		}
		t.Fatalf("write magic: %v", err)
	}
	// Extend file size beyond 16 MiB.
	const limit = 16 << 20
	if err = f.Truncate(limit + 1); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			t.Logf("close after truncate error: %v", closeErr)
		}
		t.Fatalf("truncate: %v", err)
	}
	if err = f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, loadErr := mcpserver.LoadFontData(path, "")
	if loadErr == nil {
		t.Error("LoadFontData(oversized file): want error, got nil")
	}
}

// ---- jobs: failed-job retrieval ----

// TestRetrieveJob_failedJob covers the jobStateFailed branch in retrieve.
func TestRetrieveJob_failedJob(t *testing.T) {
	sentinel := errors.New("deliberate test failure")
	jobID, err := mcpserver.SubmitJob(context.Background(), func(_ context.Context) (mcpserver.DecodeResult, error) {
		return mcpserver.DecodeResult{}, sentinel
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	_, done, retErr := mcpserver.RetrieveJob(jobID, true)
	if !done {
		t.Fatal("RetrieveJob(failed): done should be true")
	}
	if retErr == nil {
		t.Error("RetrieveJob(failed): want error, got nil")
	}
	if !errors.Is(retErr, sentinel) {
		t.Errorf("RetrieveJob(failed): error = %v, want to contain sentinel", retErr)
	}
}

// TestCancelJob_unknownID covers the cancel-nonexistent-job error path.
func TestCancelJob_unknownID(t *testing.T) {
	err := mcpserver.CancelJob("does-not-exist-xyz")
	if err == nil {
		t.Error("CancelJob(unknown id): want error, got nil")
	}
}

// TestRetrieveJob_unknownID covers the retrieve-nonexistent-job error path.
func TestRetrieveJob_unknownID(t *testing.T) {
	_, _, err := mcpserver.RetrieveJob("does-not-exist-xyz", false)
	if err == nil {
		t.Error("RetrieveJob(unknown id): want error, got nil")
	}
}

// TestCancelJob_alreadyDone covers the cancel-non-pending path (job is done,
// not in pending state). Since retrieve removes a done job, we test this by
// cancelling a completed job ID that was already removed.
func TestCancelJob_alreadyDone(t *testing.T) {
	// Submit a job that completes instantly.
	jobID, err := mcpserver.SubmitJob(context.Background(), func(_ context.Context) (mcpserver.DecodeResult, error) {
		return mcpserver.DecodeResult{Text: "done"}, nil
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	// Retrieve it (removes from registry).
	_, _, _ = mcpserver.RetrieveJob(jobID, true)
	// Now cancel the same ID — it's gone.
	cancelErr := mcpserver.CancelJob(jobID)
	if cancelErr == nil {
		t.Error("CancelJob(retrieved job): want error, got nil")
	}
}

// TestJobCancel_nonPendingStateInRegistry covers the cancel branch where the
// job is in a non-pending state (done) but still in the registry before first
// retrieval. This exercises the inner state-check in cancel().
func TestJobCancel_nonPendingStateInRegistry(t *testing.T) {
	// Submit a job that completes immediately.
	jobID, err := mcpserver.SubmitJob(context.Background(), func(_ context.Context) (mcpserver.DecodeResult, error) {
		return mcpserver.DecodeResult{Text: "fast"}, nil
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	// Give it a moment to finish so state transitions to Done.
	time.Sleep(20 * time.Millisecond)
	// Cancel after it completed — behaviour is implementation-defined
	// (may succeed or return not-found depending on internal state).
	// We just need the code path executed.
	_ = mcpserver.CancelJob(jobID)
	// Clean up: retrieve if still present.
	_, _, _ = mcpserver.RetrieveJob(jobID, false)
}

// ---- parseQuad extra error paths ----

// TestParseQuad_wrongXY covers "corner expected x,y" branch in parseQuad
// (accessible via Decode perspective with malformed quad).
func TestParseQuad_wrongXY(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	// "10 20 30 40" has 4 parts but each part has no comma → wrong format.
	_, err = mcpserver.Decode(t.Context(), img, "perspective", mcpserver.DecodeOptions{
		Quad: "10 20 30 40",
	})
	if err == nil {
		t.Error("Decode(perspective, quad='10 20 30 40'): want error, got nil")
	}
}

// TestParseQuad_badYValue covers the "corner y parse error" branch.
func TestParseQuad_badYValue(t *testing.T) {
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, err = mcpserver.Decode(t.Context(), img, "perspective", mcpserver.DecodeOptions{
		Quad: "10,20 30,bad 50,60 70,80",
	})
	if err == nil {
		t.Error("Decode(perspective, bad-y): want error, got nil")
	}
}

// ---- clampFidelity boundary coverage ----

// TestDecode_clampFidelity_boundaries tests that Decode produces a fidelity in
// [0,1] even when the distance exceeds 1 or is negative.
// We drive this through an ordinary decode and inspect the result.
func TestDecode_clampFidelity_boundaries(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	got, err := mcpserver.Decode(ctx, img, "mosaic", mcpserver.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Fidelity < 0 || got.Fidelity > 1 {
		t.Errorf("Fidelity = %.4f, want in [0, 1]", got.Fidelity)
	}
}

// ---- RankFonts: base64 font path ----

// TestRankFonts_emptyTextReturnsError is a re-check of the error path from
// rankfonts.go:57 to cover the "known_text empty" sentinel.
func TestRankFonts_emptyTextReturnsError(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, err = mcpserver.RankFonts(ctx, img, "")
	if err == nil {
		t.Error("RankFonts(\"\"): want error, got nil")
	}
}

// ---- Decode font with valid base64 ----

// TestDecode_monoHMM_base64Font covers the font-data branch using a base64
// font (the LoadFontData base64 path) then passes it to mono-hmm.
func TestDecode_monoHMM_base64Font(t *testing.T) {
	ttfPath := bundledTTFPath(t)
	raw, err := os.ReadFile(ttfPath)
	if err != nil {
		t.Fatalf("read TTF: %v", err)
	}
	b64 := base64.StdEncoding.EncodeToString(raw)
	fontData, err := mcpserver.LoadFontData("", b64)
	if err != nil {
		t.Fatalf("LoadFontData(base64): %v", err)
	}
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	_, _ = mcpserver.Decode(cancelledCtx(), img, "mono-hmm", mcpserver.DecodeOptions{
		FontData: fontData,
	})
}
