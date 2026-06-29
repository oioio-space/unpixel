package forensics

import (
	"testing"

	"github.com/oioio-space/unpixel/internal/pixelate"
)

func TestZoo_namesAndDedup(t *testing.T) {
	zoo := Zoo()
	names := map[string]bool{}
	for _, p := range zoo {
		names[p.Tool] = true
	}
	for _, want := range []string{"GEGL", "Photoshop", "CSS", "ffmpeg"} {
		if !names[want] {
			t.Errorf("Zoo() missing tool %q", want)
		}
	}
	// Two mosaic profiles with the same gamma must share a config key (dedup).
	a := Profile{Tool: "GEGL", Kind: KindMosaic, Gamma: GammaLinear}
	b := Profile{Tool: "CSS", Kind: KindMosaic, Gamma: GammaLinear}
	if a.configKey(8, 0) != b.configKey(8, 0) {
		t.Errorf("same-config profiles got different keys: %q vs %q", a.configKey(8, 0), b.configKey(8, 0))
	}
	// Different gamma → different key.
	c := Profile{Tool: "Photoshop", Kind: KindMosaic, Gamma: GammaSRGB}
	if a.configKey(8, 0) == c.configKey(8, 0) {
		t.Errorf("linear and sRGB mosaic share a key; want distinct")
	}
	// Different block size → different key even for same gamma.
	if a.configKey(8, 0) == a.configKey(16, 0) {
		t.Errorf("block 8 and block 16 share a key; want distinct")
	}
	// Blur profiles: same kernel+edge+sigma collapse; different sigma differs.
	blur1 := Profile{Tool: "ffmpeg", Kind: KindBlur, Kernel: KernelTrueGauss, Edge: pixelate.EdgeClamp}
	blur2 := Profile{Tool: "Photoshop", Kind: KindBlur, Kernel: KernelTrueGauss, Edge: pixelate.EdgeClamp}
	if blur1.configKey(0, 2.0) != blur2.configKey(0, 2.0) {
		t.Errorf("same-config blur profiles got different keys")
	}
	blur3 := Profile{Tool: "ffmpeg", Kind: KindBlur, Kernel: KernelTrueGauss, Edge: pixelate.EdgeClamp}
	if blur1.configKey(0, 2.0) == blur3.configKey(0, 3.0) {
		t.Errorf("different-sigma blur profiles share a key; want distinct")
	}
}
