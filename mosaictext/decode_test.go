package mosaictext_test

import (
	"context"
	"image"
	"image/png"
	"os"
	"runtime/debug"
	"testing"

	"github.com/oioio-space/unpixel/mosaictext"
)

// decodeResultSink absorbs benchmark results so the compiler cannot eliminate
// the Decode call.
var decodeResultSink mosaictext.Result

// guardHeap caps this process's Go heap for the duration of a test and restores
// the previous limit on cleanup. It is defence-in-depth for the memory-heavy
// zero-config decode: the shell test cage (scripts/gotest-caged.sh) does not apply
// when the test is launched from an IDE such as GoLand, which historically let a
// regression balloon to ~27 GB and OOM-freeze the whole machine. A soft heap limit
// keeps a runaway contained to a slow crawl instead of taking the desktop down.
func guardHeap(t *testing.T, bytes int64) {
	t.Helper()
	prev := debug.SetMemoryLimit(bytes)
	t.Cleanup(func() { debug.SetMemoryLimit(prev) })
}

func loadPNG(tb testing.TB, path string) image.Image {
	tb.Helper()
	f, err := os.Open(path)
	if err != nil {
		tb.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		tb.Fatalf("decode: %v", err)
	}
	return img
}

// TestDecode_HelloWorld is the zero-config end-to-end recovery: given only the
// real GIMP mosaic image, Decode reconstructs exactly "Hello World !".
func TestDecode_HelloWorld(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping zero-config mosaic decode in -short mode")
	}
	guardHeap(t, 1<<30) // backstop (~2× the ~600 MB default peak) so an IDE-launched run can't OOM-freeze the box
	img := loadPNG(t, "../testdata/real/hello-world.png")
	res, err := mosaictext.Decode(context.Background(), img)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	t.Logf("decoded %q (font=%s linear=%v block=%d N=%d phaseX=%d dist=%.2f)",
		res.Text, res.Font, res.Linear, res.BlockSize, res.CharCount, res.GridPhaseX, res.Distance)
	if res.Text != "Hello World !" {
		t.Errorf("Decode = %q, want %q", res.Text, "Hello World !")
	}
}

// BenchmarkFullDecodeSweep measures the full nine-font calibration+decode sweep
// in mosaictext.Decode on the real "Hello World !" fixture. This is the hot-path
// benchmark that quantifies the value of fontrank pre-pruning: compare
// BenchmarkFullDecodeSweep ns/op before and after wiring fontrank.
//
// The fixture is loaded once outside b.Loop() so I/O cost is not counted.
func BenchmarkFullDecodeSweep(b *testing.B) {
	img := loadPNG(b, "../testdata/real/hello-world.png")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var err error
		decodeResultSink, err = mosaictext.Decode(context.Background(), img)
		if err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}
