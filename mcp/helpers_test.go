package mcpserver_test

import (
	"image"
	_ "image/jpeg" // register JPEG decoding
	_ "image/png"  // register PNG decoding
	"os"
)

// loadFixture opens a file from the fixtures directory and decodes it as an image.
func loadFixture(name string) (image.Image, error) {
	return loadImageFile(fixturePath(name))
}

// loadSickFixture opens a file from the sick-caption fixtures directory.
func loadSickFixture(name string) (image.Image, error) {
	return loadImageFile(sickFixturePath(name))
}

func loadImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	img, _, decErr := image.Decode(f)
	if closeErr := f.Close(); closeErr != nil && decErr == nil {
		return nil, closeErr
	}
	return img, decErr
}
