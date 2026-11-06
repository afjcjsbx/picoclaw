package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// storeRef registers a fake media file and returns its media:// ref.
func storeRef(t *testing.T, store *media.FileMediaStore, path, contentType string) string {
	t.Helper()
	ref, err := store.Store(path, media.MediaMeta{ContentType: contentType, Source: "tool:inline"}, "test")
	if err != nil {
		t.Fatalf("Store() error = %v", err)
	}
	return ref
}

func writeSmallFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeTestPNG writes a valid PNG so filetype detection recognises it.
func writeTestPNG(t *testing.T) string {
	t.Helper()
	return writeBlankPNG(t, 4, 4)
}

// SVG and other vector "images" must not be inlined as data URLs: a text-only
// model is never handed an unsupported image part. They still surface as a
// path tag so file tools remain available.
func TestResolveMediaRefsSkipsInlineForSVG(t *testing.T) {
	store := media.NewFileMediaStore()
	svg := writeSmallFile(t, "logo.svg", "<svg xmlns=\"http://www.w3.org/2000/svg\"></svg>")
	ref := storeRef(t, store, svg, "image/svg+xml")

	in := []providers.Message{
		{Role: "tool", Content: "fetched page with logo", Media: []string{ref}},
	}
	out := resolveMediaRefsWithOptions(in, store, mediaResolveOptions{maxSize: 1 << 20}, 0)

	var joined string
	for _, m := range out {
		joined += m.Role + ":" + m.Content + "\n"
	}
	if strings.Contains(joined, "data:image") {
		t.Fatalf("SVG was inlined as a data URL:\n%s", joined)
	}
	if !strings.Contains(joined, "logo.svg") {
		t.Fatalf("SVG should still surface as a path tag:\n%s", joined)
	}
	if len(out) != 1 {
		t.Fatalf("expected no synthetic follow-up image message, got %d", len(out))
	}
}

// Raster images (PNG/JPEG) keep being inlined as data URLs.
func TestResolveMediaRefsInlinesRasterImages(t *testing.T) {
	store := media.NewFileMediaStore()
	png := writeTestPNG(t)
	ref := storeRef(t, store, png, "image/png")

	in := []providers.Message{
		{Role: "tool", Content: "took screenshot", Media: []string{ref}},
	}
	out := resolveMediaRefsWithOptions(in, store, mediaResolveOptions{maxSize: 1 << 20}, 0)

	var got string
	for _, m := range out {
		if len(m.Media) > 0 && strings.HasPrefix(m.Media[0], "data:image") {
			got += "INLINE:" + m.Media[0][:40] + "\n"
		} else {
			got += m.Role + ":" + m.Content + "\n"
		}
	}
	if !strings.Contains(got, "INLINE:data:image") {
		t.Fatalf("PNG should be inlined as a data URL, got:\n%s", got)
	}
}

func TestInlineImageMIME(t *testing.T) {
	for _, ok := range []string{"image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif"} {
		if !inlineImageMIME(ok) {
			t.Errorf("inlineImageMIME(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"image/svg+xml", "image/tiff", "image/avif", "image/x-icon", ""} {
		if inlineImageMIME(bad) {
			t.Errorf("inlineImageMIME(%q) = true, want false", bad)
		}
	}
}
