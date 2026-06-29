package leak

import (
	"bytes"
	"encoding/binary"
	"image/jpeg"
)

// maxThumbBytes caps the thumbnail size to 2 MiB (security bound on hostile input).
const maxThumbBytes = 2 << 20

// exifThumbnail attempts to recover an EXIF IFD1 embedded JPEG thumbnail from
// JPEG data. It walks the APP1 segment, parses the TIFF header, reads IFD0 to
// find IFD1, then extracts the thumbnail via tags 0x0201/0x0202. It abstains
// (returns false) on any malformation — it never panics on hostile input.
func exifThumbnail(data []byte) (Result, bool) {
	tiff, ok := findEXIFBlock(data)
	if !ok {
		return Result{}, false
	}

	order, ok := byteOrder(tiff)
	if !ok {
		return Result{}, false
	}

	// TIFF header: 2-byte order mark + 2-byte magic (0x002A) + 4-byte IFD0 offset.
	if len(tiff) < 8 {
		return Result{}, false
	}
	if order.Uint16(tiff[2:4]) != 0x002A {
		return Result{}, false
	}
	ifd0Off := int(order.Uint32(tiff[4:8]))

	ifd1Off, ok := readIFD0NextOffset(tiff, ifd0Off, order)
	if !ok || ifd1Off == 0 {
		return Result{}, false
	}

	thumbOff, thumbLen, ok := readIFD1ThumbTags(tiff, ifd1Off, order)
	if !ok {
		return Result{}, false
	}

	// Cap and bounds-check the thumbnail slice.
	if thumbLen > maxThumbBytes {
		thumbLen = maxThumbBytes
	}
	end := thumbOff + thumbLen
	if thumbOff < 0 || end > len(tiff) || end < thumbOff {
		return Result{}, false
	}
	thumb := tiff[thumbOff:end]

	img, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		return Result{}, false
	}
	return Result{
		Source:     SourceEXIFThumbnail,
		Image:      img,
		Confidence: 1.0,
	}, true
}

// findEXIFBlock scans JPEG markers from offset 2 (after SOI FFD8) looking for
// an APP1 segment whose payload begins with "Exif\x00\x00". It returns the TIFF
// block (payload after the 6-byte Exif header) or false if not found.
// It stops scanning at SOS (FFDA) — segments after the scan have no EXIF.
func findEXIFBlock(data []byte) ([]byte, bool) {
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		return nil, false
	}
	pos := 2
	for pos+3 < len(data) {
		if data[pos] != 0xFF {
			return nil, false
		}
		marker := data[pos+1]
		if marker == 0xDA { // SOS — no APP1 found before scan data
			return nil, false
		}
		// Segment length is big-endian and includes its own 2 bytes.
		segLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		if segLen < 2 {
			return nil, false
		}
		payloadStart := pos + 4
		payloadEnd := pos + 2 + segLen
		if payloadEnd > len(data) {
			return nil, false
		}

		if marker == 0xE1 { // APP1
			payload := data[payloadStart:payloadEnd]
			const exifHeader = "Exif\x00\x00"
			if len(payload) >= len(exifHeader) && string(payload[:len(exifHeader)]) == exifHeader {
				return payload[len(exifHeader):], true
			}
		}
		pos = payloadEnd
	}
	return nil, false
}

// byteOrder reads the TIFF byte-order mark ("II" or "MM") and returns the
// corresponding binary.ByteOrder.
func byteOrder(tiff []byte) (binary.ByteOrder, bool) {
	if len(tiff) < 2 {
		return nil, false
	}
	switch string(tiff[:2]) {
	case "II":
		return binary.LittleEndian, true
	case "MM":
		return binary.BigEndian, true
	default:
		return nil, false
	}
}

// readIFD0NextOffset reads the 4-byte next-IFD pointer that follows IFD0's
// entries. ifd0Off is relative to the start of tiff.
func readIFD0NextOffset(tiff []byte, ifd0Off int, order binary.ByteOrder) (int, bool) {
	if ifd0Off < 0 || ifd0Off+2 > len(tiff) {
		return 0, false
	}
	entryCount := int(order.Uint16(tiff[ifd0Off : ifd0Off+2]))
	// Each IFD entry is 12 bytes; next-IFD pointer follows all entries.
	nextOff := ifd0Off + 2 + entryCount*12
	if nextOff+4 > len(tiff) {
		return 0, false
	}
	return int(order.Uint32(tiff[nextOff : nextOff+4])), true
}

// readIFD1ThumbTags scans IFD1 entries for tags 0x0201 (JPEGInterchangeFormat)
// and 0x0202 (JPEGInterchangeFormatLength). Both must be present.
// All offsets are relative to the TIFF origin (start of tiff).
func readIFD1ThumbTags(tiff []byte, ifd1Off int, order binary.ByteOrder) (off, length int, ok bool) {
	if ifd1Off < 0 || ifd1Off+2 > len(tiff) {
		return 0, 0, false
	}
	entryCount := int(order.Uint16(tiff[ifd1Off : ifd1Off+2]))
	pos := ifd1Off + 2

	var thumbOff, thumbLen int
	var hasOff, hasLen bool
	for range entryCount {
		if pos+12 > len(tiff) {
			return 0, 0, false
		}
		tag := order.Uint16(tiff[pos : pos+2])
		val := int(order.Uint32(tiff[pos+8 : pos+12]))
		switch tag {
		case 0x0201:
			thumbOff = val
			hasOff = true
		case 0x0202:
			thumbLen = val
			hasLen = true
		}
		pos += 12
	}
	if !hasOff || !hasLen {
		return 0, 0, false
	}
	return thumbOff, thumbLen, true
}
