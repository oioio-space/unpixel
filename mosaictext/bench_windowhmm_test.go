package mosaictext

// bench_windowhmm_test.go — real-data benchmark for DecodeWindowHMM.
//
// This benchmark loads the production sick_man_playing.png fixture and runs
// the full DecodeWindowHMM pipeline so that a CPU profile reflects actual
// hot-path costs rather than synthetic uniform-random data.
//
// Usage (single-image, with profile):
//
//	./scripts/gotest-caged.sh go test -run '^$' \
//	  -bench=BenchmarkDecodeWindowHMMReal \
//	  -benchtime=1x -count=1 \
//	  -cpuprofile=/tmp/whmm-real.prof \
//	  ./mosaictext/
//	go tool pprof -top -nodecount=20 /tmp/whmm-real.prof

import (
	"image/png"
	"os"
	"testing"
)

// sinkWHMMResult absorbs the DecodeWindowHMM result so the compiler cannot
// eliminate the call.
var sinkWHMMResult Result

// BenchmarkDecodeWindowHMMReal runs DecodeWindowHMM on the real
// sick_man_playing.png fixture (text: "a man is playing a guitar",
// charset: lowercase + space). It is the representative real-decode workload
// (~32 s/op) that drives window-HMM optimisation decisions.
//
// Run with -benchtime=1x -count=6; compare baselines with:
//
//	mise run bench:baseline
//	# make changes
//	mise run bench:compare
func BenchmarkDecodeWindowHMMReal(b *testing.B) {
	const fixturePath = "../testdata/sick/sick_man_playing.png"
	f, err := os.Open(fixturePath)
	if err != nil {
		b.Skipf("sick_man_playing.png unavailable: %v", err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		b.Fatalf("png.Decode: %v", err)
	}

	const charset = "abcdefghijklmnopqrstuvwxyz "

	b.ReportAllocs()
	var res Result
	for b.Loop() {
		res, err = DecodeWindowHMM(
			b.Context(),
			img,
			WithWHMMCharset(charset),
		)
		if err != nil {
			b.Fatalf("DecodeWindowHMM: %v", err)
		}
	}
	// Log the decoded text so changes in output across optimisation passes are
	// immediately visible in benchmark output. (The decoder does not currently
	// achieve the ground truth on this image; the assertion would be wrong.)
	b.Logf("decoded: %q (ground truth: %q)", res.Text, "a man is playing a guitar")
	sinkWHMMResult = res
}
