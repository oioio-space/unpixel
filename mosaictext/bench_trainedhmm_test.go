package mosaictext

// bench_trainedhmm_test.go — real-data benchmark for DecodeTrainedHMM.
//
// This benchmark loads the production sick_man_playing.png fixture and runs
// the full DecodeTrainedHMM pipeline (train + Viterbi) so that benchmark
// numbers reflect actual wall-clock cost rather than synthetic data.
//
// Usage (single-image, with profile):
//
//	./scripts/gotest-caged.sh go test -run '^$' \
//	  -bench=BenchmarkDecodeTrainedHMMReal \
//	  -benchtime=1x -count=6 \
//	  -cpuprofile=/tmp/thmm-real.prof \
//	  ./mosaictext/
//	go tool pprof -top -nodecount=20 /tmp/thmm-real.prof

import (
	"image/png"
	"os"
	"testing"

	"github.com/oioio-space/unpixel/internal/lang"
)

// sinkTHMMResult absorbs the DecodeTrainedHMM result so the compiler cannot
// eliminate the call.
var sinkTHMMResult Result

// BenchmarkDecodeTrainedHMMReal runs DecodeTrainedHMM on the real
// sick_man_playing.png fixture (text: "a man is playing a guitar",
// charset: lowercase + space) with the same options the journal decoder test
// uses (WithTHMMLanguage(lang.English)). It is the representative real-decode
// workload that drives trained-HMM optimisation decisions.
//
// Run with -benchtime=1x -count=6; compare baselines with:
//
//	mise run bench:baseline
//	# make changes
//	mise run bench:compare
func BenchmarkDecodeTrainedHMMReal(b *testing.B) {
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
		res, err = DecodeTrainedHMM(
			b.Context(),
			img,
			WithTHMMCharset(charset),
			WithTHMMLanguage(lang.English),
		)
		if err != nil {
			b.Fatalf("DecodeTrainedHMM: %v", err)
		}
	}
	// Log the decoded text so changes in output across optimisation passes are
	// immediately visible in benchmark output.
	b.Logf("decoded: %q (ground truth: %q)", res.Text, "a man is playing a guitar")
	sinkTHMMResult = res
}
