package agent

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// exifSegment builds an APP1 segment whose IFD0 holds only an Orientation tag.
func exifSegment(o int, order binary.AppendByteOrder) []byte {
	tiff := []byte("II")
	if order == binary.BigEndian {
		tiff = []byte("MM")
	}
	tiff = order.AppendUint16(tiff, 42)
	tiff = order.AppendUint32(tiff, 8) // IFD0 right after the header
	tiff = order.AppendUint16(tiff, 1) // one entry
	tiff = order.AppendUint16(tiff, 0x0112)
	tiff = order.AppendUint16(tiff, 3) // SHORT
	tiff = order.AppendUint32(tiff, 1)
	tiff = order.AppendUint16(tiff, uint16(o))
	tiff = order.AppendUint16(tiff, 0) // value padding
	tiff = order.AppendUint32(tiff, 0) // no IFD1

	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xff, 0xe1}
	seg = binary.BigEndian.AppendUint16(seg, uint16(len(payload)+2))
	return append(seg, payload...)
}

// withSegment returns jpegData with seg inserted straight after the SOI marker.
func withSegment(jpegData, seg []byte) []byte {
	out := append([]byte{}, jpegData[:2]...)
	out = append(out, seg...)
	return append(out, jpegData[2:]...)
}

func TestJPEGOrientation(t *testing.T) {
	data, err := os.ReadFile(writeTestJPEG(t, 16, 8))
	if err != nil {
		t.Fatal(err)
	}

	if got := jpegOrientation(bytes.NewReader(data)); got != 1 {
		t.Errorf("no EXIF: got %d, want 1", got)
	}
	for _, o := range []int{3, 6, 8} {
		for name, order := range map[string]binary.AppendByteOrder{"LE": binary.LittleEndian, "BE": binary.BigEndian} {
			got := jpegOrientation(bytes.NewReader(withSegment(data, exifSegment(o, order))))
			if got != o {
				t.Errorf("orientation %d (%s): got %d", o, name, got)
			}
		}
	}

	// An XMP APP1 ahead of the EXIF one must not stop the scan.
	xmp := []byte{0xff, 0xe1, 0x00, 0x08, 'x', 'm', 'p', 0, 0, 0}
	got := jpegOrientation(bytes.NewReader(withSegment(withSegment(data, exifSegment(6, binary.LittleEndian)), xmp)))
	if got != 6 {
		t.Errorf("EXIF after XMP: got %d, want 6", got)
	}

	// Out-of-range values and truncated input fall back to upright.
	if got := jpegOrientation(bytes.NewReader(withSegment(data, exifSegment(9, binary.LittleEndian)))); got != 1 {
		t.Errorf("invalid value: got %d, want 1", got)
	}
	if got := jpegOrientation(bytes.NewReader(data[:5])); got != 1 {
		t.Errorf("truncated: got %d, want 1", got)
	}
}

func TestOrient(t *testing.T) {
	// 3×2 source, pixel values 1..6 in the red channel:
	//   1 2 3
	//   4 5 6
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for i := 0; i < 6; i++ {
		src.SetNRGBA(i%3, i/3, color.NRGBA{R: uint8(i + 1), A: 255})
	}

	tests := []struct {
		orientation int
		want        [][]uint8
	}{
		{1, [][]uint8{{1, 2, 3}, {4, 5, 6}}},
		{2, [][]uint8{{3, 2, 1}, {6, 5, 4}}},
		{3, [][]uint8{{6, 5, 4}, {3, 2, 1}}},
		{4, [][]uint8{{4, 5, 6}, {1, 2, 3}}},
		{5, [][]uint8{{1, 4}, {2, 5}, {3, 6}}},
		{6, [][]uint8{{4, 1}, {5, 2}, {6, 3}}},
		{7, [][]uint8{{6, 3}, {5, 2}, {4, 1}}},
		{8, [][]uint8{{3, 6}, {2, 5}, {1, 4}}},
	}
	for _, tt := range tests {
		got := orient(src, tt.orientation)
		b := got.Bounds()
		if b.Dy() != len(tt.want) || b.Dx() != len(tt.want[0]) {
			t.Errorf("orientation %d: size %dx%d, want %dx%d",
				tt.orientation, b.Dx(), b.Dy(), len(tt.want[0]), len(tt.want))
			continue
		}
		for y, row := range tt.want {
			for x, want := range row {
				if r := got.NRGBAAt(x, y).R; r != want {
					t.Errorf("orientation %d: pixel (%d,%d) = %d, want %d", tt.orientation, x, y, r, want)
				}
			}
		}
	}
}

func TestDownscaleToDataURL_AppliesJPEGOrientation(t *testing.T) {
	data, err := os.ReadFile(writeTestJPEG(t, 800, 400))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rotated.jpg")
	if err := os.WriteFile(path, withSegment(data, exifSegment(6, binary.LittleEndian)), 0o600); err != nil {
		t.Fatal(err)
	}

	dataURL, ok := downscaleToDataURL(path, "image/jpeg", testVisionOpts(100))
	if !ok {
		t.Fatal("expected downscale to succeed")
	}
	// Stored 2:1 landscape, tagged "rotate 90° clockwise": displays 1:2 portrait.
	if b := decodeDataURL(t, dataURL).Bounds(); b.Dx() != 50 || b.Dy() != 100 {
		t.Fatalf("downscaled image is %dx%d, want 50x100 after applying EXIF rotation", b.Dx(), b.Dy())
	}
}
