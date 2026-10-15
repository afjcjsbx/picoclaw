// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"strings"

	"github.com/afjcjsbx/picoclaw/pkg/logger"
)

// maxDecodePixels caps the decoded size of an image we are willing to
// decompress for downscaling. File size is no guard: a few-KB PNG can declare
// tens of thousands of pixels per side and expand to gigabytes when decoded.
// 64 Mpx is at most ~256 MB as NRGBA, enough for any camera photo.
const maxDecodePixels = 64 << 20

// downscaleToDataURL decodes an image file, downsamples it so that its longest
// edge is at most maxDim pixels, and returns it as a base64 data URL together
// with the MIME type of the encoded output. It reports ok=false when the image
// is already small enough, cannot be decoded, uses an unsupported format, or
// the result still exceeds opts.maxSize — in which case the caller should fall
// back to sending the original file.
//
// Downscaling before base64-encoding is the single largest lever on the
// transient memory used by the vision pipeline: a 20 MB photo becomes a few
// hundred kilobytes, shrinking both the encoded payload held in the message
// history and the JSON request built for the model.
func downscaleToDataURL(localPath, mime string, opts mediaResolveOptions) (string, bool) {
	maxDim := opts.maxImageDimension
	if maxDim <= 0 {
		return "", false
	}
	// Only formats whose decoder is linked in (image/jpeg, image/png). GIF is
	// deliberately absent: it is not registered, and flattening an animated
	// GIF to a single JPEG frame would silently drop the animation.
	switch mime {
	case "image/jpeg", "image/jpg", "image/png":
	default:
		return "", false
	}

	f, err := os.Open(localPath)
	if err != nil {
		return "", false
	}
	defer f.Close()

	// Read only the header first: images already within the cap are skipped
	// without decoding, and ones declaring absurd dimensions are rejected
	// before any pixel buffer is allocated.
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return "", false
	}
	// Already within the cap: nothing to do, and the pixels never get decoded.
	if cfg.Width <= maxDim && cfg.Height <= maxDim {
		return "", false
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxDecodePixels {
		logger.WarnCF("agent", "Image too large to downscale, sending original", map[string]any{
			"path":   localPath,
			"width":  cfg.Width,
			"height": cfg.Height,
		})
		return "", false
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return "", false
	}

	src, format, err := image.Decode(f)
	if err != nil {
		return "", false
	}

	b := src.Bounds()
	nw, nh := scaledDimensions(b.Dx(), b.Dy(), maxDim)
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	boxResample(dst, src)

	if format == "jpeg" {
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return "", false
		}
		dst = orient(dst, jpegOrientation(f))
	}

	var buf bytes.Buffer
	var outMIME string
	switch format {
	case "png":
		if err := png.Encode(&buf, dst); err != nil {
			return "", false
		}
		outMIME = "image/png"
	default:
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: opts.jpegQuality}); err != nil {
			return "", false
		}
		outMIME = "image/jpeg"
	}

	// The size limit applies to what is actually sent, not to the original.
	if buf.Len() > opts.maxSize {
		return "", false
	}

	var sb strings.Builder
	sb.Grow(len("data:;base64,") + len(outMIME) + base64.StdEncoding.EncodedLen(buf.Len()))
	sb.WriteString("data:")
	sb.WriteString(outMIME)
	sb.WriteString(";base64,")
	sb.WriteString(base64.StdEncoding.EncodeToString(buf.Bytes()))
	return sb.String(), true
}

// scaledDimensions returns the target size preserving aspect ratio, with both
// edges at least 1 pixel.
func scaledDimensions(w, h, maxDim int) (int, int) {
	longest := w
	if h > longest {
		longest = h
	}
	if longest <= maxDim {
		return w, h
	}
	nw := w * maxDim / longest
	nh := h * maxDim / longest
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return nw, nh
}

// nrgbaReader returns an accessor for src's pixels as non-premultiplied NRGBA.
// image.Image.At boxes every pixel into a color.Color interface and the model
// conversion boxes it again, which for a 12 Mpx photo is tens of millions of
// short-lived heap allocations. The concrete types the JPEG and PNG decoders
// produce are read directly instead; anything else takes the generic path.
func nrgbaReader(src image.Image) func(x, y int) color.NRGBA {
	switch s := src.(type) {
	case *image.YCbCr:
		return func(x, y int) color.NRGBA {
			c := s.YCbCrAt(x, y)
			r, g, b := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
			return color.NRGBA{R: r, G: g, B: b, A: 0xff}
		}
	case *image.NRGBA:
		return s.NRGBAAt
	case *image.RGBA:
		return func(x, y int) color.NRGBA {
			c := s.RGBAAt(x, y)
			switch c.A {
			case 0xff:
				return color.NRGBA{R: c.R, G: c.G, B: c.B, A: 0xff}
			case 0:
				return color.NRGBA{}
			}
			a := uint32(c.A)
			return color.NRGBA{
				R: uint8(uint32(c.R) * 0xff / a),
				G: uint8(uint32(c.G) * 0xff / a),
				B: uint8(uint32(c.B) * 0xff / a),
				A: c.A,
			}
		}
	case *image.Gray:
		return func(x, y int) color.NRGBA {
			v := s.GrayAt(x, y).Y
			return color.NRGBA{R: v, G: v, B: v, A: 0xff}
		}
	default:
		return func(x, y int) color.NRGBA {
			return color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
		}
	}
}

// boxResample averages each destination pixel over the corresponding source
// rectangle. This is a straightforward, dependency-free downsampling filter
// suitable for the large reduction factors used here (source >> target).
func boxResample(dst *image.NRGBA, src image.Image) {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	db := dst.Bounds()
	dw, dh := db.Dx(), db.Dy()
	at := nrgbaReader(src)

	for dy := 0; dy < dh; dy++ {
		sy0 := sb.Min.Y + dy*sh/dh
		sy1 := sb.Min.Y + (dy+1)*sh/dh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			sx0 := sb.Min.X + dx*sw/dw
			sx1 := sb.Min.X + (dx+1)*sw/dw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}

			// Weight color by alpha so fully transparent pixels, whose RGB is
			// arbitrary (often black), do not bleed into the edges of opaque
			// content. 64-bit sums cannot overflow even for extreme reductions.
			var r, g, bl, a, n uint64
			for y := sy0; y < sy1; y++ {
				for x := sx0; x < sx1; x++ {
					c := at(x, y)
					ca := uint64(c.A)
					r += uint64(c.R) * ca
					g += uint64(c.G) * ca
					bl += uint64(c.B) * ca
					a += ca
					n++
				}
			}
			if n == 0 || a == 0 {
				continue // dst starts zeroed: fully transparent
			}
			half := a / 2
			dst.SetNRGBA(db.Min.X+dx, db.Min.Y+dy, color.NRGBA{
				R: uint8((r + half) / a),
				G: uint8((g + half) / a),
				B: uint8((bl + half) / a),
				A: uint8((a + n/2) / n),
			})
		}
	}
}
