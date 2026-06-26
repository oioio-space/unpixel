package mcpserver_test

import (
	"testing"

	mcpserver "github.com/oioio-space/unpixel/mcp"
)

// TestDecode_auto verifies that method=auto on the block08_go fixture
// returns a non-empty text and a non-negative distance.
func TestDecode_auto(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "auto", mcpserver.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode(auto): %v", err)
	}
	if got.Text == "" {
		t.Error("Decode(auto): Text is empty")
	}
	if got.Distance < 0 {
		t.Errorf("Decode(auto): Distance = %.4f, want >= 0", got.Distance)
	}
	if got.MethodUsed == "" {
		t.Error("Decode(auto): MethodUsed is empty")
	}
}

// TestDecode_mosaic verifies that method=mosaic recovers "go" from block08_go.png.
func TestDecode_mosaic(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "mosaic", mcpserver.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode(mosaic): %v", err)
	}
	if got.Text != "go" {
		t.Errorf("Decode(mosaic): Text = %q, want %q", got.Text, "go")
	}
	if got.MethodUsed != "mosaic" {
		t.Errorf("Decode(mosaic): MethodUsed = %q, want %q", got.MethodUsed, "mosaic")
	}
	if got.Distance < 0 {
		t.Errorf("Decode(mosaic): Distance = %.4f, want >= 0", got.Distance)
	}
}

// TestDecode_unknownMethod verifies that an unknown method returns an error.
func TestDecode_unknownMethod(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	_, err = mcpserver.Decode(ctx, img, "nonexistent-method", mcpserver.DecodeOptions{})
	if err == nil {
		t.Error("Decode(nonexistent-method): want error, got nil")
	}
}

// TestDecode_fidelity verifies that Fidelity = 1 - Distance for a mosaic decode.
func TestDecode_fidelity(t *testing.T) {
	ctx := t.Context()
	img, err := loadFixture("block08_go.png")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}

	got, err := mcpserver.Decode(ctx, img, "mosaic", mcpserver.DecodeOptions{})
	if err != nil {
		t.Fatalf("Decode(mosaic): %v", err)
	}
	want := 1 - got.Distance
	if want < 0 {
		want = 0
	}
	const tol = 1e-9
	if got.Fidelity < want-tol || got.Fidelity > want+tol {
		t.Errorf("Fidelity = %.6f, want 1-Distance = %.6f", got.Fidelity, want)
	}
}
