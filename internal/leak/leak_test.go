package leak

import (
	"os"
	"testing"
)

func TestSniff(t *testing.T) {
	tests := []struct {
		name string
		head []byte
		want fileKind
	}{
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE1}, kindJPEG},
		{"pdf", []byte("%PDF-1.7\n"), kindPDF},
		{"zip", []byte{'P', 'K', 0x03, 0x04}, kindZIP},
		{"png", []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, kindPNG},
		{"unknown", []byte{0x00, 0x01, 0x02, 0x03}, kindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sniff(tc.head); got != tc.want {
				t.Errorf("sniff(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestScan_unknownNoLeak(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/x.bin"
	if err := os.WriteFile(p, []byte{0, 1, 2, 3, 4, 5}, 0o600); err != nil {
		t.Fatal(err)
	}
	_, found, err := Scan(p, Options{})
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if found {
		t.Errorf("found = true, want false for unknown file")
	}
}
