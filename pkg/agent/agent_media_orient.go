// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"bufio"
	"encoding/binary"
	"image"
	"io"
)

// jpegOrientation returns the EXIF orientation (1-8) of a JPEG stream, or 1
// (upright) when the tag is absent or the header cannot be parsed. Phone
// cameras store photos in sensor orientation and record the rotation only in
// this tag; image/jpeg ignores it and re-encoding drops it, so a downscaled
// copy must have the rotation baked in or it would appear sideways.
func jpegOrientation(r io.Reader) int {
	br := bufio.NewReader(r)
	var soi [2]byte
	if _, err := io.ReadFull(br, soi[:]); err != nil || soi != [2]byte{0xff, 0xd8} {
		return 1
	}
	for {
		var hdr [4]byte // marker (2) + segment length (2, includes itself)
		if _, err := io.ReadFull(br, hdr[:]); err != nil || hdr[0] != 0xff {
			return 1
		}
		marker, n := hdr[1], int(binary.BigEndian.Uint16(hdr[2:]))-2
		if n < 0 || marker == 0xda || marker == 0xd9 { // SOS/EOI: no metadata follows
			return 1
		}
		if marker != 0xe1 { // APP1 carries EXIF (and XMP, which we skip)
			if _, err := br.Discard(n); err != nil {
				return 1
			}
			continue
		}
		seg := make([]byte, n)
		if _, err := io.ReadFull(br, seg); err != nil {
			return 1
		}
		if o, ok := exifOrientation(seg); ok {
			return o
		}
	}
}

// exifOrientation reads the Orientation tag (0x0112) from an APP1 payload.
// ok is false when the segment is not EXIF, so the caller keeps scanning.
func exifOrientation(seg []byte) (orientation int, ok bool) {
	if len(seg) < 14 || string(seg[:6]) != "Exif\x00\x00" {
		return 1, false
	}
	t := seg[6:] // TIFF header: byte order, magic, offset of IFD0
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1, true
	}
	ifd := int(bo.Uint32(t[4:8]))
	if ifd < 8 || ifd+2 > len(t) {
		return 1, true
	}
	for i, count := 0, int(bo.Uint16(t[ifd:])); i < count; i++ {
		e := ifd + 2 + i*12 // 12-byte entries: tag, type, count, value
		if e+12 > len(t) {
			break
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v, true
			}
			break
		}
	}
	return 1, true
}

// orient returns src transformed so that an image stored with the given EXIF
// orientation displays upright. Orientations 5-8 swap width and height.
func orient(src *image.NRGBA, orientation int) *image.NRGBA {
	if orientation <= 1 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	ow, oh := w, h
	if orientation >= 5 {
		ow, oh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < oh; y++ {
		for x := 0; x < ow; x++ {
			var sx, sy int // source pixel that lands on (x, y)
			switch orientation {
			case 2: // mirror horizontally
				sx, sy = w-1-x, y
			case 3: // rotate 180
				sx, sy = w-1-x, h-1-y
			case 4: // mirror vertically
				sx, sy = x, h-1-y
			case 5: // transpose
				sx, sy = y, x
			case 6: // rotate 90 clockwise
				sx, sy = y, h-1-x
			case 7: // transverse
				sx, sy = w-1-y, h-1-x
			case 8: // rotate 90 counter-clockwise
				sx, sy = w-1-y, x
			}
			dst.SetNRGBA(x, y, src.NRGBAAt(b.Min.X+sx, b.Min.Y+sy))
		}
	}
	return dst
}
