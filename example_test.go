package unpixel_test

import (
	"context"
	"fmt"
	"image"
	"image/color"

	"github.com/oioio-space/unpixel"
	_ "github.com/oioio-space/unpixel/defaults" // wire standard components
)

// Example demonstrates the quick-start usage of UnPixel: construct an Engine
// from a pixelated image, run the search, consume progress events, then collect
// results. The image here is a small synthetic white rectangle used purely to
// keep the example self-contained and fast; in practice, supply the actual
// pixelated screenshot region.
func Example() {
	// Build a tiny synthetic image to stand in for a real pixelated screenshot.
	img := image.NewRGBA(image.Rect(0, 0, 64, 16))
	for y := range img.Bounds().Dy() {
		for x := range img.Bounds().Dx() {
			img.SetRGBA(x, y, color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
		}
	}

	eng, err := unpixel.New(img, unpixel.Config{
		Charset:   "ab ",
		MaxLength: 2,
		BlockSize: unpixel.DefaultBlockSize,
	})
	if err != nil {
		fmt.Println("new:", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	progCh, resCh := eng.Run(ctx)

	// Consume progress events concurrently while collecting results below.
	// OnProgress blocks until the progress channel is closed (EventDone).
	done := make(chan struct{})
	go func() {
		defer close(done)
		unpixel.OnProgress(progCh, func(p unpixel.Progress) {
			if p.Kind == unpixel.EventNewBest {
				fmt.Println("new best:", p.BestGuess)
			}
		})
	}()

	for r := range resCh {
		if r.Err != nil {
			fmt.Println("result error:", r.Err)
			continue
		}
		fmt.Println("result:", r.BestGuess)
	}
	<-done
}

// ExampleRecoverBytes recovers redacted text from an image already in memory (an
// HTTP body, an embedded asset), constraining the search to digits for a numeric
// secret such as a PIN — the narrowest charset gives the fastest, most reliable
// search.
func ExampleRecoverBytes() {
	var pngData []byte // the mosaic image bytes

	res, err := unpixel.RecoverBytes(context.Background(), pngData,
		unpixel.WithCharset(unpixel.CharsetDigits),
		unpixel.WithBlockSize(8),
	)
	if err != nil {
		fmt.Println("recover:", err)
		return
	}
	fmt.Println(res.BestGuess)
}

// ExampleVerifyBytes confirms proposed candidate strings against a redaction using
// the faithful forward model — the propose-and-verify path. WithVerifyThreshold
// tunes how close a physical fit must be to count as a Match.
func ExampleVerifyBytes() {
	var redaction []byte // the mosaic image bytes

	verdicts, err := unpixel.VerifyBytes(context.Background(), redaction,
		[]string{"hunter2", "swordfish"},
		unpixel.WithVerifyThreshold(0.05),
	)
	if err != nil {
		fmt.Println("verify:", err)
		return
	}
	for _, v := range verdicts {
		if v.Match {
			fmt.Printf("%s confirmed (distance %.4f)\n", v.Text, v.Distance)
		}
	}
}
