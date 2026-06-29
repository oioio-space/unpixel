package forensics

import (
	"image"
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

// srgbMosaic builds a two-tone checkerboard then mosaics it with block-average
// (sRGB colorspace) to produce a deterministic test fixture.
func srgbMosaic(block int) *image.RGBA {
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := byte(255)
			if (x/3+y/3)%2 == 0 {
				c = 0
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	return pixelate.NewBlockAverage(block).Pixelate(src, 0, 0)
}

// gaussianBlurFixture builds a hard black/white edge image then applies a
// Gaussian blur at the given sigma, matching the pattern in detectblur_test.go.
func gaussianBlurFixture(sigma float64) *image.RGBA {
	const w, h = 48, 24
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := byte(255)
			if x >= w/2 {
				c = 0
			}
			i := src.PixOffset(x, y)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = c, c, c, 255
		}
	}
	return pixelate.NewGaussianBlur(sigma).Pixelate(src, 0, 0)
}

func TestFingerprint_srgbMosaic(t *testing.T) {
	op := Fingerprint(srgbMosaic(8), Hint{Block: 8})
	if op.Kind != KindMosaic {
		t.Errorf("Kind = %v, want KindMosaic", op.Kind)
	}
	if op.Block != 8 {
		t.Errorf("Block = %d, want 8", op.Block)
	}
	if op.Gamma != GammaSRGB {
		t.Errorf("Gamma = %v, want GammaSRGB", op.Gamma)
	}
}

func TestFingerprint_gaussianBlur(t *testing.T) {
	const sigma = 3.0
	op := Fingerprint(gaussianBlurFixture(sigma), Hint{})

	if op.Kind != KindBlur {
		t.Fatalf("Kind = %v, want KindBlur", op.Kind)
	}
	if op.Sigma <= 0 {
		t.Errorf("Sigma = %v, want > 0", op.Sigma)
	}
	if op.Conf.Kind <= 0 {
		t.Errorf("Conf.Kind = %v, want > 0 (some detection confidence)", op.Conf.Kind)
	}
}

func TestOperatorBuild_thresholdGate(t *testing.T) {
	op := Operator{Kind: KindMosaic, Gamma: GammaLinear, Block: 8, Conf: Conf{Kind: 0.9, Gamma: 0.9}}
	if px, ok := op.Build(0.5); !ok || px == nil {
		t.Errorf("Build(0.5) ok=%v px=%v, want ok=true non-nil", ok, px)
	}
	low := Operator{Kind: KindMosaic, Gamma: GammaLinear, Block: 8, Conf: Conf{Kind: 0.2, Gamma: 0.2}}
	if _, ok := low.Build(0.5); ok {
		t.Errorf("Build(0.5) on low-confidence op = ok, want ok=false (fallback)")
	}
	// Conf.Kind below threshold even though Conf.Gamma is above — must gate.
	lowKind := Operator{Kind: KindMosaic, Gamma: GammaLinear, Block: 8, Conf: Conf{Kind: 0.2, Gamma: 0.9}}
	if _, ok := lowKind.Build(0.5); ok {
		t.Errorf("Build(0.5) with low Conf.Kind = ok, want ok=false (§5 per-attribute gate)")
	}
}

func TestOperatorBuild_blurBranch(t *testing.T) {
	tests := []struct {
		name      string
		op        Operator
		threshold float64
		wantOK    bool
	}{
		{
			name:      "TrueGauss above threshold",
			op:        Operator{Kind: KindBlur, Sigma: 3, Kernel: KernelTrueGauss, Conf: Conf{Kind: 0.99}},
			threshold: 0.5,
			wantOK:    true,
		},
		{
			name:      "Box3 above threshold",
			op:        Operator{Kind: KindBlur, Sigma: 3, Kernel: KernelBox3, Conf: Conf{Kind: 0.99}},
			threshold: 0.5,
			wantOK:    true,
		},
		{
			name:      "zero Sigma rejects",
			op:        Operator{Kind: KindBlur, Sigma: 0, Kernel: KernelTrueGauss, Conf: Conf{Kind: 0.99}},
			threshold: 0.5,
			wantOK:    false,
		},
		{
			name:      "Conf.Kind below threshold rejects",
			op:        Operator{Kind: KindBlur, Sigma: 3, Kernel: KernelTrueGauss, Conf: Conf{Kind: 0.3}},
			threshold: 0.5,
			wantOK:    false,
		},
		{
			name:      "KindUnknown rejects",
			op:        Operator{Kind: KindUnknown, Sigma: 3, Conf: Conf{Kind: 0.99}},
			threshold: 0.5,
			wantOK:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.op.Build(tc.threshold)
			if ok != tc.wantOK {
				t.Errorf("Build(%v) ok = %v, want %v", tc.threshold, ok, tc.wantOK)
			}
			if tc.wantOK && got == nil {
				t.Errorf("Build(%v) returned nil Pixelator, want non-nil", tc.threshold)
			}
			if !tc.wantOK && got != nil {
				t.Errorf("Build(%v) returned non-nil Pixelator, want nil", tc.threshold)
			}
		})
	}
}

func TestKind_String(t *testing.T) {
	tests := []struct {
		kind Kind
		want string
	}{
		{kind: KindUnknown, want: "unknown"},
		{kind: KindMosaic, want: "mosaic"},
		{kind: KindBlur, want: "blur"},
		{kind: Kind(99), want: "unknown"},
	}
	for _, tc := range tests {
		got := tc.kind.String()
		if got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestGamma_String(t *testing.T) {
	tests := []struct {
		gamma Gamma
		want  string
	}{
		{gamma: GammaUnknown, want: "unknown"},
		{gamma: GammaSRGB, want: "sRGB"},
		{gamma: GammaLinear, want: "linear"},
		{gamma: Gamma(99), want: "unknown"},
	}
	for _, tc := range tests {
		got := tc.gamma.String()
		if got != tc.want {
			t.Errorf("Gamma(%d).String() = %q, want %q", tc.gamma, got, tc.want)
		}
	}
}

func TestKernel_String(t *testing.T) {
	tests := []struct {
		kernel Kernel
		want   string
	}{
		{kernel: KernelUnknown, want: "unknown"},
		{kernel: KernelTrueGauss, want: "true-gauss"},
		{kernel: KernelBox3, want: "box3"},
		{kernel: Kernel(99), want: "unknown"},
	}
	for _, tc := range tests {
		got := tc.kernel.String()
		if got != tc.want {
			t.Errorf("Kernel(%d).String() = %q, want %q", tc.kernel, got, tc.want)
		}
	}
}

// TestBuildBlur_toolLabels verifies that Build exercises blurTool and mapKernel
// for each kernel path and returns a functional Pixelator (non-nil, invokable).
func TestBuildBlur_toolLabels(t *testing.T) {
	tests := []struct {
		name     string
		kernel   Kernel
		wantTool string
	}{
		{name: "TrueGauss tool label", kernel: KernelTrueGauss, wantTool: "Photoshop"},
		{name: "Box3 tool label", kernel: KernelBox3, wantTool: "GIMP/CSS"},
	}

	const w, h = 16, 16
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range src.Pix {
		src.Pix[i] = 255
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			op := Operator{
				Kind:   KindBlur,
				Sigma:  3,
				Kernel: tc.kernel,
				Tool:   blurTool(tc.kernel),
				Conf:   Conf{Kind: 0.99},
			}
			if got := op.Tool; got != tc.wantTool {
				t.Errorf("Tool = %q, want %q", got, tc.wantTool)
			}
			px, ok := op.Build(0.5)
			if !ok || px == nil {
				t.Fatalf("Build(0.5) ok=%v px=%v, want ok=true non-nil", ok, px)
			}
			out := px.Pixelate(src, 0, 0)
			if out == nil {
				t.Errorf("Pixelate returned nil")
			}
		})
	}
}

// TestMosaicBuild_sRGBPath exercises the Build mosaic+sRGB branch (Block valid,
// Gamma=GammaSRGB) and asserts a non-nil Pixelator is returned.
func TestMosaicBuild_sRGBPath(t *testing.T) {
	op := Operator{
		Kind:  KindMosaic,
		Gamma: GammaSRGB,
		Block: 8,
		Tool:  mosaicTool(GammaSRGB),
		Conf:  Conf{Kind: 0.9, Gamma: 0.9},
	}

	if got := op.Tool; got != "Photoshop/GIMP" {
		t.Errorf("mosaicTool(GammaSRGB) = %q, want %q", got, "Photoshop/GIMP")
	}

	px, ok := op.Build(0.5)
	if !ok || px == nil {
		t.Fatalf("Build(0.5) ok=%v px=%v, want ok=true non-nil", ok, px)
	}
}

// TestMosaicBuild_linearPath exercises the Build mosaic+linear branch and asserts
// mosaicTool returns the correct label for GammaLinear.
func TestMosaicBuild_linearPath(t *testing.T) {
	op := Operator{
		Kind:  KindMosaic,
		Gamma: GammaLinear,
		Block: 8,
		Tool:  mosaicTool(GammaLinear),
		Conf:  Conf{Kind: 0.9, Gamma: 0.9},
	}

	if got := op.Tool; got != "GEGL/CSS" {
		t.Errorf("mosaicTool(GammaLinear) = %q, want %q", got, "GEGL/CSS")
	}
}

// TestMosaicTool_unknownGamma exercises the mosaicTool default branch.
func TestMosaicTool_unknownGamma(t *testing.T) {
	got := mosaicTool(GammaUnknown)
	want := ""
	if got != want {
		t.Errorf("mosaicTool(GammaUnknown) = %q, want %q", got, want)
	}
}

// TestBlurTool_unknownKernel exercises the blurTool default branch.
func TestBlurTool_unknownKernel(t *testing.T) {
	got := blurTool(KernelUnknown)
	want := ""
	if got != want {
		t.Errorf("blurTool(KernelUnknown) = %q, want %q", got, want)
	}
}

// TestMapKernel covers all three mapKernel cases.
func TestMapKernel(t *testing.T) {
	tests := []struct {
		in   pixelate.BlurKernel
		want Kernel
	}{
		{in: pixelate.BlurKernelTrueGauss, want: KernelTrueGauss},
		{in: pixelate.BlurKernelBox3, want: KernelBox3},
		{in: pixelate.BlurKernelUnknown, want: KernelUnknown},
		{in: pixelate.BlurKernel(99), want: KernelUnknown},
	}
	for _, tc := range tests {
		got := mapKernel(tc.in)
		if got != tc.want {
			t.Errorf("mapKernel(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestMosaicBuild_smallBlock verifies that a mosaic operator with Block < 2 rejects.
func TestMosaicBuild_smallBlock(t *testing.T) {
	op := Operator{Kind: KindMosaic, Gamma: GammaSRGB, Block: 1, Conf: Conf{Kind: 0.9, Gamma: 0.9}}
	if _, ok := op.Build(0.5); ok {
		t.Errorf("Build(0.5) with Block=1 = ok, want ok=false")
	}
}

var sinkOp Operator

func BenchmarkFingerprint(b *testing.B) {
	img := srgbMosaic(8)
	b.ReportAllocs()
	for b.Loop() {
		sinkOp = Fingerprint(img, Hint{Block: 8})
	}
}
