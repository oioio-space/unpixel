package mcpserver_test

import (
	"slices"
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestCalibrate_runsWithoutError verifies that Calibrate completes without
// error on a real fixture with known text. We do not assert a specific axis
// value because the Nunito font may not match Liberation Sans; we only check
// that the call returns a valid report.
func TestCalibrate_runsWithoutError(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	report, err := mcpserver.Calibrate(img, "hello", mcpserver.CalibrateOptions{})
	if err != nil {
		t.Fatalf("Calibrate: %v", err)
	}
	if len(report.FittedAxes) == 0 {
		t.Error("Calibrate: FittedAxes is empty")
	}
	if report.Distance < 0 {
		t.Errorf("Calibrate: Distance = %.4f, want >= 0", report.Distance)
	}
	if report.Evals <= 0 {
		t.Errorf("Calibrate: Evals = %d, want > 0", report.Evals)
	}
}

// TestCalibrate_emptyTextError verifies that an empty visible_text returns an error.
func TestCalibrate_emptyTextError(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	_, err = mcpserver.Calibrate(img, "", mcpserver.CalibrateOptions{})
	if err == nil {
		t.Error("Calibrate(emptyText): want error, got nil")
	}
}

// TestCalibrate_robotoFlexMultiAxis verifies that Calibrate with font=robotoflex
// and axes=[wght, slnt] returns two fitted axes, both tagged correctly.
func TestCalibrate_robotoFlexMultiAxis(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	report, err := mcpserver.Calibrate(img, "hello", mcpserver.CalibrateOptions{
		Font: "robotoflex",
		Axes: []string{"wght", "slnt"},
	})
	if err != nil {
		t.Fatalf("Calibrate(robotoflex, wght+slnt): %v", err)
	}
	if len(report.FittedAxes) != 2 {
		t.Errorf("FittedAxes: got %d, want 2", len(report.FittedAxes))
	}

	tags := make([]string, len(report.FittedAxes))
	for i, a := range report.FittedAxes {
		tags[i] = a.Tag
	}
	for _, want := range []string{"wght", "slnt"} {
		if !slices.Contains(tags, want) {
			t.Errorf("FittedAxes missing tag %q; got %v", want, tags)
		}
	}
	if report.Distance < 0 {
		t.Errorf("Distance = %.4f, want >= 0", report.Distance)
	}
	if report.Evals <= 0 {
		t.Errorf("Evals = %d, want > 0", report.Evals)
	}
}

// TestCalibrate_unsupportedAxisError verifies that requesting an axis not
// present in the chosen font returns a clear error.
func TestCalibrate_unsupportedAxisError(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	// Nunito only has wght; requesting opsz should fail.
	_, err = mcpserver.Calibrate(img, "hello", mcpserver.CalibrateOptions{
		Font: "nunito",
		Axes: []string{"opsz"},
	})
	if err == nil {
		t.Error("Calibrate(nunito, opsz): want error for unsupported axis, got nil")
	}
}

// TestCalibrate_unknownFontError verifies that an unrecognised font name
// returns a clear error.
func TestCalibrate_unknownFontError(t *testing.T) {
	img, err := loadFixture("text_hello.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	_, err = mcpserver.Calibrate(img, "hello", mcpserver.CalibrateOptions{
		Font: "inter",
	})
	if err == nil {
		t.Error("Calibrate(inter): want error for unknown font, got nil")
	}
}
