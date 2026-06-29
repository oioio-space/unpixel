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

// TestExifThumbnail_truncatedAPP1 exercises the bounds-check abstain path: an
// APP1 segment whose declared length exceeds the actual data triggers abstain.
func TestExifThumbnail_truncatedAPP1(t *testing.T) {
	// Build a JPEG whose APP1 length field claims 1000 bytes but has only 10.
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xD8})                          // SOI
	buf.Write([]byte{0xFF, 0xE1})                          // APP1 marker
	_ = binary.Write(&buf, binary.BigEndian, uint16(1000)) // lies about length
	buf.Write(make([]byte, 10))                            // only 10 payload bytes
	if _, found := exifThumbnail(buf.Bytes()); found {
		t.Error("exifThumbnail found=true on truncated APP1, want false (abstain)")
	}
}

// TestByteOrder_invalid verifies that byteOrder abstains on an unknown marker.
func TestByteOrder_invalid(t *testing.T) {
	_, ok := byteOrder([]byte("XX"))
	if ok {
		t.Error("byteOrder ok=true on invalid marker, want false")
	}
}

// TestByteOrder_tooShort verifies that byteOrder abstains on short input.
func TestByteOrder_tooShort(t *testing.T) {
	_, ok := byteOrder([]byte("I"))
	if ok {
		t.Error("byteOrder ok=true on 1-byte input, want false")
	}
}

// TestReadIFD0NextOffset_outOfBounds verifies bounds abstain for the IFD0 entry
// block running off the end of the TIFF data.
func TestReadIFD0NextOffset_outOfBounds(t *testing.T) {
	// A TIFF fragment with IFD0 entry count of 100 but only 4 bytes of data.
	tiff := make([]byte, 4)
	binary.LittleEndian.PutUint16(tiff[0:2], 100) // claims 100 entries
	_, ok := readIFD0NextOffset(tiff, 0, binary.LittleEndian)
	if ok {
		t.Error("readIFD0NextOffset ok=true when entries exceed data, want false")
	}
}

// TestReadIFD0NextOffset_negativeOff verifies abstain on a negative offset.
func TestReadIFD0NextOffset_negativeOff(t *testing.T) {
	_, ok := readIFD0NextOffset([]byte{0, 0, 0, 0}, -1, binary.LittleEndian)
	if ok {
		t.Error("readIFD0NextOffset ok=true on negative offset, want false")
	}
}

// TestExifThumbnail_badTIFFMagic verifies abstain when the TIFF magic word is wrong.
func TestExifThumbnail_badTIFFMagic(t *testing.T) {
	// Build a JPEG with an Exif APP1 whose TIFF header has wrong magic (0x0000).
	var tiff bytes.Buffer
	tiff.WriteString("II")
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x0000)) // bad magic
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8))

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8})
	out.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(&out, binary.BigEndian, uint16(len(payload)+2))
	out.Write(payload)
	out.Write([]byte{0xFF, 0xD9})
	if _, found := exifThumbnail(out.Bytes()); found {
		t.Error("exifThumbnail found=true on bad TIFF magic, want false")
	}
}

// TestExifThumbnail_noIFD1 verifies abstain when IFD0 next-IFD pointer is zero
// (no IFD1 present).
func TestExifThumbnail_noIFD1(t *testing.T) {
	var tiff bytes.Buffer
	tiff.WriteString("II")
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x002A))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8)) // IFD0 at offset 8
	// IFD0: 0 entries, next-IFD = 0 (no IFD1).
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0)) // entry count
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(0)) // next-IFD = 0

	payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var out bytes.Buffer
	out.Write([]byte{0xFF, 0xD8})
	out.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(&out, binary.BigEndian, uint16(len(payload)+2))
	out.Write(payload)
	out.Write([]byte{0xFF, 0xD9})
	if _, found := exifThumbnail(out.Bytes()); found {
		t.Error("exifThumbnail found=true when IFD1 pointer is zero, want false")
	}
}
