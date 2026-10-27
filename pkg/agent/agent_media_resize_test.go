package agent

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testVisionOpts(maxDim int) mediaResolveOptions {
	return mediaResolveOptions{maxSize: 20 << 20, maxImageDimension: maxDim, jpegQuality: 85}
}

func writeTestJPEG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	path := filepath.Join(t.TempDir(), "test.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp image: %v", err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode temp image: %v", err)
	}
	return path
}

// writeBlankPNG writes a valid w×h 1-bit grayscale PNG. All-zero scanlines
// compress to almost nothing, so a huge declared size costs little on disk.
func writeBlankPNG(t *testing.T, w, h int) string {
	t.Helper()

	var idat bytes.Buffer
	zw := zlib.NewWriter(&idat)
	row := make([]byte, 1+(w+7)/8)
	for y := 0; y < h; y++ {
		if _, err := zw.Write(row); err != nil {
			t.Fatalf("compress scanline: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zlib: %v", err)
	}

	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(typ string, data []byte) {
		_ = binary.Write(&out, binary.BigEndian, uint32(len(data)))
		out.WriteString(typ)
		out.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(typ))
		crc.Write(data)
		_ = binary.Write(&out, binary.BigEndian, crc.Sum32())
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8] = 1 // bit depth; color type 0 (grayscale) is the zero value
	chunk("IHDR", ihdr)
	chunk("IDAT", idat.Bytes())
	chunk("IEND", nil)

	path := filepath.Join(t.TempDir(), "blank.png")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		t.Fatalf("write temp png: %v", err)
	}
	return path
}

func decodeDataURL(t *testing.T, dataURL string) image.Image {
	t.Helper()
	idx := strings.Index(dataURL, ",")
	if idx < 0 {
		t.Fatalf("data URL has no comma: %q", dataURL)
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[idx+1:])
	if err != nil {
		t.Fatalf("decode data URL: %v", err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode image: %v", err)
	}
	return img
}

func TestDownscaleToDataURL_CapsLongestEdge(t *testing.T) {
	path := writeTestJPEG(t, 800, 400)

	dataURL, ok := downscaleToDataURL(path, "image/jpeg", testVisionOpts(100))
	if !ok {
		t.Fatal("expected downscale to succeed for oversized image")
	}
	if !strings.HasPrefix(dataURL, "data:image/jpeg;base64,") {
		t.Fatalf("unexpected data URL prefix: %q", dataURL[:40])
	}

	img := decodeDataURL(t, dataURL)
	b := img.Bounds()
	if b.Dx() > 100 || b.Dy() > 100 {
		t.Fatalf("downscaled image is %dx%d, want longest edge <= 100", b.Dx(), b.Dy())
	}
	// Aspect ratio 2:1 preserved (within rounding).
	if b.Dx() != 100 || b.Dy() != 50 {
		t.Fatalf("downscaled image is %dx%d, want 100x50", b.Dx(), b.Dy())
	}
}

func TestDownscaleToDataURL_SkipsSmallImages(t *testing.T) {
	path := writeTestJPEG(t, 80, 60)

	if _, ok := downscaleToDataURL(path, "image/jpeg", testVisionOpts(100)); ok {
		t.Fatal("expected downscale to be skipped for an image already within the cap")
	}
}

func TestDownscaleToDataURL_RejectsOversizedPixelCount(t *testing.T) {
	// Control: the generated PNG is valid and downscales when small enough.
	if _, ok := downscaleToDataURL(writeBlankPNG(t, 200, 200), "image/png", testVisionOpts(100)); !ok {
		t.Fatal("test PNG generator produced an undecodable image")
	}

	// 16384×16384 = 268 Mpx, well over maxDecodePixels, yet only tens of KB
	// on disk. It must be refused from the header alone, never decoded.
	path := writeBlankPNG(t, 16384, 16384)

	if _, ok := downscaleToDataURL(path, "image/png", testVisionOpts(100)); ok {
		t.Fatal("expected image above maxDecodePixels to be left to the raw fallback")
	}
}

func TestDownscaleToDataURL_SkipsUnsupportedMIME(t *testing.T) {
	path := writeTestJPEG(t, 800, 400)

	for _, mime := range []string{"image/gif", "image/webp", "application/pdf"} {
		if _, ok := downscaleToDataURL(path, mime, testVisionOpts(100)); ok {
			t.Errorf("expected %s to be left to the raw fallback", mime)
		}
	}
}

func TestDownscaleToDataURL_Disabled(t *testing.T) {
	path := writeTestJPEG(t, 800, 400)

	if _, ok := downscaleToDataURL(path, "image/jpeg", testVisionOpts(0)); ok {
		t.Fatal("expected downscale to be skipped when maxDim is 0")
	}
}

func TestScalingDimensions(t *testing.T) {
	tests := []struct {
		w, h, max, wantW, wantH int
	}{
		{800, 400, 100, 100, 50},
		{400, 800, 100, 50, 100},
		{100, 100, 100, 100, 100},
		{3, 1000, 10, 1, 10},
	}
	for _, tt := range tests {
		gotW, gotH := scaledDimensions(tt.w, tt.h, tt.max)
		if gotW != tt.wantW || gotH != tt.wantH {
			t.Errorf("scaledDimensions(%d,%d,%d) = %d,%d; want %d,%d",
				tt.w, tt.h, tt.max, gotW, gotH, tt.wantW, tt.wantH)
		}
	}
}

func TestDownscaleToDataURL_RejectsResultOverMaxSize(t *testing.T) {
	path := writeTestJPEG(t, 800, 400)

	opts := testVisionOpts(100)
	opts.maxSize = 1 // nothing fits
	if _, ok := downscaleToDataURL(path, "image/jpeg", opts); ok {
		t.Fatal("expected downscaled payload above maxSize to be refused")
	}
}

func TestEncodeImageToDataURL_DownscalesOriginalOverMaxSize(t *testing.T) {
	path := writeTestJPEG(t, 800, 400)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Limit sits between the downscaled payload and the original file size.
	opts := testVisionOpts(100)
	opts.maxSize = int(info.Size()) - 1
	if got := encodeImageToDataURL(path, "image/jpeg", info, opts); got == "" {
		t.Fatal("original over maxSize should be inlined once downscaled under it")
	}

	// Without downscaling the same limit still drops the original.
	opts.maxImageDimension = 0
	if got := encodeImageToDataURL(path, "image/jpeg", info, opts); got != "" {
		t.Fatal("original over maxSize must be dropped when downscaling is disabled")
	}
}

// opaqueImage hides the concrete image type so boxResample takes the generic
// At/Convert path, giving a reference result for the typed fast paths.
type opaqueImage struct{ image.Image }

func TestBoxResample_FastPathsMatchGenericPath(t *testing.T) {
	const w, h = 64, 48

	ycbcr := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	for i := range ycbcr.Y {
		ycbcr.Y[i] = uint8(i * 7)
	}
	for i := range ycbcr.Cb {
		ycbcr.Cb[i] = uint8(i*3 + 40)
		ycbcr.Cr[i] = uint8(i*5 + 90)
	}
	nrgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	gray := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			nrgba.SetNRGBA(x, y, color.NRGBA{uint8(x * 4), uint8(y * 5), uint8(x + y), uint8(x*3 + 20)})
			rgba.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: uint8(255 - x)})
			gray.SetGray(x, y, color.Gray{Y: uint8(x*3 + y)})
		}
	}

	for name, src := range map[string]image.Image{
		"ycbcr": ycbcr, "nrgba": nrgba, "rgba": rgba, "gray": gray,
	} {
		t.Run(name, func(t *testing.T) {
			fast := image.NewNRGBA(image.Rect(0, 0, 16, 12))
			ref := image.NewNRGBA(image.Rect(0, 0, 16, 12))
			boxResample(fast, src)
			boxResample(ref, opaqueImage{src})
			for i := range fast.Pix {
				// The typed paths round in 8 bits, the generic one in 16.
				if d := int(fast.Pix[i]) - int(ref.Pix[i]); d < -2 || d > 2 {
					t.Fatalf("pix[%d] = %d, generic path gives %d", i, fast.Pix[i], ref.Pix[i])
				}
			}
		})
	}
}

func TestBoxResample_TransparentPixelsDoNotBleedColor(t *testing.T) {
	// Left pixel: opaque red. Right pixel: fully transparent with black RGB,
	// as PNG encoders commonly store it.
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	src.SetNRGBA(1, 0, color.NRGBA{})

	dst := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	boxResample(dst, src)

	got := dst.NRGBAAt(0, 0)
	if got.R != 255 || got.G != 0 || got.B != 0 {
		t.Fatalf("color = %v, want pure red: transparent pixel must not darken it", got)
	}
	if got.A < 127 || got.A > 128 {
		t.Fatalf("alpha = %d, want ~128 (half coverage)", got.A)
	}
}

func TestBoxResample_FullyTransparentStaysTransparent(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	dst := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	boxResample(dst, src)

	for i, v := range dst.Pix {
		if v != 0 {
			t.Fatalf("pix[%d] = %d, want 0", i, v)
		}
	}
}
