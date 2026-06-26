package mcpserver_test

import (
	"image"
	_ "image/jpeg" // register JPEG decoding
	_ "image/png"  // register PNG decoding
	"os"
)

// loadFixture opens a file from the fixtures directory and decodes it as an image.
func loadFixture(name string) (image.Image, error) {
	f, err := os.Open(fixturePath(name))
	if err != nil {
		return nil, err
	}
	img, _, decErr := image.Decode(f)
	if closeErr := f.Close(); closeErr != nil && decErr == nil {
		return nil, closeErr
	}
	return img, decErr
}
