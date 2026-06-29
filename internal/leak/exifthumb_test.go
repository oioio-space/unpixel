package leak

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"testing"
)

// makeThumbJPEG encodes a tiny solid-colour JPEG used as the embedded thumbnail.
func makeThumbJPEG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range im.Pix {
		im.Pix[i] = 0xAA
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, im, &jpeg.Options{Quality: 75}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// makeEXIFJPEGOrder builds a minimal JPEG (FFD8 + APP1[Exif/TIFF/IFD0→IFD1] + FFD9)
// whose IFD1 carries thumb as JPEGInterchangeFormat, using the given TIFF byte
// order (binary.LittleEndian with marker "II", or binary.BigEndian with "MM").
func makeEXIFJPEGOrder(t *testing.T, thumb []byte, order binary.ByteOrder, marker string) []byte {
	t.Helper()
	// TIFF block (origin = start of this block).
	var tiff bytes.Buffer
	tiff.WriteString(marker)
	_ = binary.Write(&tiff, order, uint16(0x002A)) // magic
	_ = binary.Write(&tiff, order, uint32(8))      // IFD0 at offset 8
	// IFD0: 0 entries, next-IFD offset → IFD1.
	_ = binary.Write(&tiff, order, uint16(0)) // entry count 0
	ifd1Off := uint32(8 + 2 + 4)              // after IFD0 (count+nextoff)
	_ = binary.Write(&tiff, order, ifd1Off)   // next IFD = IFD1
	// IFD1: 2 entries (0x0201 offset, 0x0202 length), next-IFD 0, then thumb bytes.
	_ = binary.Write(&tiff, order, uint16(2)) // 2 entries
	ifd1End := ifd1Off + 2 + 2*12 + 4         // count + 2 entries + nextoff
	thumbOff := ifd1End
	writeEntry := func(tag, typ uint16, count, val uint32) {
		_ = binary.Write(&tiff, order, tag)
		_ = binary.Write(&tiff, order, typ)
		_ = binary.Write(&tiff, order, count)
		_ = binary.Write(&tiff, order, val)
	}
	writeEntry(0x0201, 4, 1, thumbOff)           // JPEGInterchangeFormat (LONG)
	writeEntry(0x0202, 4, 1, uint32(len(thumb))) // JPEGInterchangeFormatLength
	_ = binary.Write(&tiff, order, uint32(0))    // no next IFD
	tiff.Write(thumb)

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8})                                    // SOI
	out.Write([]byte{0xFF, 0xE1})                                    // APP1
	_ = binary.Write(&out, binary.BigEndian, uint16(len(payload)+2)) // APP1 segment length is always big-endian
	out.Write(payload)
	out.Write([]byte{0xFF, 0xD9}) // EOI
	return out.Bytes()
}

// makeEXIFJPEG builds a little-endian ("II") EXIF JPEG with an embedded thumbnail.
func makeEXIFJPEG(t *testing.T, thumb []byte) []byte {
	t.Helper()
	return makeEXIFJPEGOrder(t, thumb, binary.LittleEndian, "II")
}

func TestExifThumbnail_recovers(t *testing.T) {
	thumb := makeThumbJPEG(t)
	data := makeEXIFJPEG(t, thumb)
	res, found := exifThumbnail(data)
	if !found {
		t.Fatalf("exifThumbnail found=false, want true")
	}
	if res.Source != SourceEXIFThumbnail {
		t.Errorf("Source = %q, want %q", res.Source, SourceEXIFThumbnail)
	}
	if res.Image == nil || res.Image.Bounds().Dx() != 8 {
		t.Errorf("recovered image = %v, want 8x8 thumbnail", res.Image)
	}
}

// TestExifThumbnail_bigEndian exercises the MM (big-endian) TIFF branch, which a
// real Canon-style JPEG would use — the security-sensitive byte-order path.
func TestExifThumbnail_bigEndian(t *testing.T) {
	thumb := makeThumbJPEG(t)
	data := makeEXIFJPEGOrder(t, thumb, binary.BigEndian, "MM")
	res, found := exifThumbnail(data)
	if !found {
		t.Fatalf("exifThumbnail(MM) found=false, want true")
	}
	if res.Image == nil || res.Image.Bounds().Dx() != 8 {
		t.Errorf("recovered image = %v, want 8x8 thumbnail (big-endian TIFF)", res.Image)
	}
}

func TestExifThumbnail_noExifAbstains(t *testing.T) {
	plain := makeThumbJPEG(t) // a JPEG with no APP1/EXIF
	if _, found := exifThumbnail(plain); found {
		t.Errorf("found=true on JPEG without EXIF thumbnail, want false")
	}
}
