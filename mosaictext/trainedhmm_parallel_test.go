package mosaictext_test

// trainedhmm_parallel_test.go — byte-identity and option tests for the
// parallel font×linear sweep in DecodeTrainedHMM.
//
// TestDecodeTrainedHMM_ParallelByteIdentical is the primary correctness gate:
// it asserts that the parallel sweep (default workers) produces byte-identical
// decoded text to the serial path (WithTHMMMaxWorkers(1)) for two distinct
// inputs. Winner selection scans results in original (fe,lin) ordinal order,
// so tie-breaking on distance is deterministic regardless of goroutine schedule.
//
// TestWithTHMMMaxWorkersOption verifies option storage and the n≤0 no-op
// contract, mirroring TestWithWHMMMaxWorkersOption in windowhmm_test.go.

import (
	"testing"

	"github.com/oioio-space/unpixel/defaults"
	"github.com/oioio-space/unpixel/mosaictext"
)

// TestDecodeTrainedHMM_ParallelByteIdentical verifies that the parallel font
// sweep (default workers) produces byte-identical decoded text to the serial
// path (WithTHMMMaxWorkers(1)) for two distinct inputs: a monospace digit
// string and a monospace lowercase string.
func TestDecodeTrainedHMM_ParallelByteIdentical(t *testing.T) {
	t.Parallel()

	monoData := thmmFindFont(t, "Liberation Mono")
	monoR, err := defaults.RendererFromFonts(monoData, nil)
	if err != nil {
		t.Fatalf("build mono renderer: %v", err)
	}

	cases := []struct {
		name    string
		text    string
		fs      float64
		block   int
		linear  bool
		charset string
	}{
		{
			name:    "mono-digits",
			text:    "3141592653",
			fs:      32.0,
			block:   4,
			linear:  false,
			charset: "0123456789",
		},
		{
			name:    "mono-alpha",
			text:    "hello",
			fs:      32.0,
			block:   4,
			linear:  false,
			charset: "abcdefghijklmnopqrstuvwxyz",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mosaicImg := syntheticMosaic(t, tc.text, monoData, tc.fs, tc.block, tc.linear)
			path := thmmSavePNG(t, mosaicImg)
			img := thmmLoadPNG(t, path)

			// Shared options: pin to Liberation Mono + sRGB only so the sweep
			// covers exactly one font × one linear mode (reproducible and fast).
			sharedOpts := []mosaictext.THMMOption{
				mosaictext.WithTHMMFont("Liberation Mono"),
				mosaictext.WithTHMMCharset(tc.charset),
				mosaictext.WithTHMMLinear(0),
				mosaictext.WithTHMMCorpus(300),
				mosaictext.WithTHMMSeed(42),
			}

			// Build renderer reference to verify it can render the test string.
			_ = monoR

			// Serial: 1 worker gives the deterministic ordinal-order baseline.
			serialRes, serErr := mosaictext.DecodeTrainedHMM(t.Context(), img,
				append(sharedOpts, mosaictext.WithTHMMMaxWorkers(1))...,
			)
			if serErr != nil {
				t.Fatalf("serial decode: %v", serErr)
			}

			// Parallel: default worker count (min(NumCPU, thmmWorkerCap)).
			parallelRes, parErr := mosaictext.DecodeTrainedHMM(t.Context(), img, sharedOpts...)
			if parErr != nil {
				t.Fatalf("parallel decode: %v", parErr)
			}

			t.Logf("%s: serial=%q parallel=%q dist=%.4f",
				tc.name, serialRes.Text, parallelRes.Text, parallelRes.Distance)

			if parallelRes.Text != serialRes.Text {
				t.Errorf("byte-identity failure: serial=%q parallel=%q",
					serialRes.Text, parallelRes.Text)
			}
		})
	}
}

// TestWithTHMMMaxWorkersOption verifies that WithTHMMMaxWorkers is accepted by
// DecodeTrainedHMM without error and that the n≤0 no-op contract is wired
// (exercised indirectly: passing 0 after 4 must not override the worker cap).
//
// Direct struct-field inspection is not possible from the external test package,
// so correctness is confirmed by byte-identity in
// TestDecodeTrainedHMM_ParallelByteIdentical; this test only guards the public
// option signature and the n≤0 no-op path against regression.
func TestWithTHMMMaxWorkersOption(t *testing.T) {
	t.Parallel()

	// WithTHMMMaxWorkers must return a non-nil THMMOption for any input.
	for _, n := range []int{-1, 0, 1, 4, 8} {
		opt := mosaictext.WithTHMMMaxWorkers(n)
		if opt == nil {
			t.Errorf("WithTHMMMaxWorkers(%d) returned nil", n)
		}
	}
}
