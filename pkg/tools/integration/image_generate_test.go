package integrationtools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/tools"
	"github.com/sipeed/picoclaw/pkg/utils"
)

func TestImageGenerateUsesModelProxy(t *testing.T) {
	var proxyCalled atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalled.Store(true)
		if r.URL.String() != "http://image-model.invalid/v1/images/generations" {
			t.Errorf("request URL = %q", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprintf(
			w,
			`{"data":[{"b64_json":%q}]}`,
			base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n")),
		)
	}))
	defer proxy.Close()

	cfg := config.DefaultConfig()
	cfg.Tools.ImageGenerate.ModelName = "image-model"
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "image-model",
		Model:     "openai/gpt-image-1",
		APIBase:   "http://image-model.invalid/v1",
		Proxy:     proxy.URL,
		APIKeys:   config.SimpleSecureStrings("key"),
	}}
	store := media.NewFileMediaStore()
	defer store.ReleaseAll("tool:image_generate:test:chat")
	tool := NewImageGenerateTool(cfg, config.DefaultMaxMediaSize, store)
	result := tool.Execute(tools.WithToolContext(context.Background(), "test", "chat"), map[string]any{"prompt": "cat"})
	if result.IsError {
		t.Fatalf("image generation failed: %s", result.ForLLM)
	}
	if !proxyCalled.Load() || len(result.Media) != 1 {
		t.Fatalf("proxy called = %t, media refs = %d", proxyCalled.Load(), len(result.Media))
	}
}

func TestOpenRouterImageUsesBaseRequest(t *testing.T) {
	image := []byte("\x89PNG\r\n\x1a\n")
	for _, model := range []string{"meta/muse-image", "recraft/recraft-v4"} {
		t.Run(model, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != http.MethodPost || req.URL.String() != "https://openrouter.ai/api/v1/images" {
					t.Errorf("request = %s %s", req.Method, req.URL)
				}
				if req.Header.Get("Authorization") != "Bearer key" {
					t.Errorf("authorization = %q", req.Header.Get("Authorization"))
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if len(body) != 2 || body["model"] != model || body["prompt"] != "cat" {
					t.Errorf("request body = %#v, want only model and prompt", body)
				}
				response := fmt.Sprintf(`{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(image))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte(response))),
				}, nil
			})}
			var buf bytes.Buffer
			err := NewImageGenerateTool(nil, config.DefaultMaxMediaSize, nil).generateOpenRouterImage(
				context.Background(), client, "https://openrouter.ai/api/v1", "key", model, "cat", &buf,
			)
			if err != nil || !bytes.Equal(buf.Bytes(), image) {
				t.Fatalf("image = %v, error = %v", buf.Bytes(), err)
			}
		})
	}
}

func TestImageDownloadRejectsNonPublicTargets(t *testing.T) {
	dial := utils.NewSafeDialContext(&net.Dialer{Timeout: 5 * time.Second}, nil, nil)
	for _, host := range []string{
		"localhost",
		"127.0.0.1",
		"10.0.0.1",
		"169.254.169.254",
		"0.0.0.0",
		"0.1.2.3",
		"100.64.0.1",
		"198.18.0.1",
		"::1",
		"fc00::1",
		// 6to4 and Teredo wrap an embedded IPv4 address.
		"2002:0a00:0001::1",
		"2001:0000:4136:e378:8000:63bf:f5ff:fffe",
	} {
		if isPublicImageHost(host) {
			t.Errorf("accepted non-public host %q", host)
		}
		_, err := dial(context.Background(), "tcp", net.JoinHostPort(host, "443"))
		if err == nil {
			t.Errorf("dialed non-public host %q", host)
		}
	}
	if !isPublicImageHost("8.8.8.8") {
		t.Error("rejected public IP")
	}
}

func TestImageGenerateAllowsConfiguredResponseSize(t *testing.T) {
	response := bytes.Repeat([]byte("x"), 33<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(response)
	}))
	defer server.Close()

	tool := NewImageGenerateTool(nil, 25<<20, nil)
	got, err := tool.postJSON(context.Background(), server.Client(), server.URL, "", "", nil)
	if err != nil {
		t.Fatalf("postJSON rejected response within configured limit: %v", err)
	}
	if len(got) != len(response) {
		t.Fatalf("response length = %d, want %d", len(got), len(response))
	}
}

func TestImageSink_EnforcesLimitAndCapturesHeader(t *testing.T) {
	var buf bytes.Buffer
	sink := newImageSink(&buf, 8)
	if _, err := sink.Write([]byte("12345")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := sink.Write([]byte("67890")); err == nil {
		t.Fatal("expected write past the limit to fail")
	}
	if buf.String() != "12345" {
		t.Fatalf("wrote %q, want only the accepted bytes", buf.String())
	}
	if string(sink.header) != "12345" {
		t.Fatalf("header = %q, want the leading accepted bytes only", sink.header)
	}
}

func TestWriteBase64Image_StreamsToWriter(t *testing.T) {
	image := []byte("\x89PNG\r\n\x1a\npayload")
	var buf bytes.Buffer
	if err := writeBase64Image(&buf, base64.StdEncoding.EncodeToString(image)); err != nil {
		t.Fatalf("writeBase64Image: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), image) {
		t.Fatalf("decoded = %v, want %v", buf.Bytes(), image)
	}
}

func TestWriteBase64Image_ErrorWording(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 64))
	tests := []struct {
		name       string
		encoded    string
		limit      int
		wantDecode bool
		wantTooBig bool
	}{
		{"malformed base64", "!!!not-base64!!!", 1024, true, false},
		{"truncated base64", valid[:len(valid)-1], 1024, true, false},
		{"image over the limit", valid, 16, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := writeBase64Image(newImageSink(io.Discard, tt.limit), tt.encoded)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := strings.Contains(err.Error(), "failed to decode image data"); got != tt.wantDecode {
				t.Fatalf("error = %q, decode wording = %t, want %t", err, got, tt.wantDecode)
			}
			if got := errors.Is(err, errGeneratedImageTooLarge); got != tt.wantTooBig {
				t.Fatalf("error = %q, too large = %t, want %t", err, got, tt.wantTooBig)
			}
		})
	}
}

func TestGenerateOpenRouterImage_RejectsOversized(t *testing.T) {
	image := bytes.Repeat([]byte("x"), 4096)
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		response := fmt.Sprintf(`{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(image))
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader([]byte(response))),
		}, nil
	})}
	sink := newImageSink(io.Discard, 16) // smaller than the decoded image
	err := NewImageGenerateTool(nil, config.DefaultMaxMediaSize, nil).generateOpenRouterImage(
		context.Background(), client, "https://openrouter.ai/api/v1", "key", "m", "cat", sink,
	)
	if !errors.Is(err, errGeneratedImageTooLarge) {
		t.Fatalf("error = %v, want errGeneratedImageTooLarge", err)
	}
}

// isolateTempDir points os.TempDir at a fresh directory so a test can assert
// which picoclaw-generated-* files exist after the tool runs.
func isolateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	return dir
}

func generatedFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "picoclaw-generated-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return files
}

// newProxiedImageTool returns an ImageGenerateTool whose OpenAI-compatible
// image requests are answered by handler through the model proxy.
func newProxiedImageTool(
	t *testing.T,
	model string,
	maxSize int,
	handler http.HandlerFunc,
) (*ImageGenerateTool, *media.FileMediaStore) {
	t.Helper()
	proxy := httptest.NewServer(handler)
	t.Cleanup(proxy.Close)

	cfg := config.DefaultConfig()
	cfg.Tools.ImageGenerate.ModelName = "image-model"
	cfg.ModelList = []*config.ModelConfig{{
		ModelName: "image-model",
		Model:     model,
		APIBase:   "http://image-model.invalid/v1",
		Proxy:     proxy.URL,
		APIKeys:   config.SimpleSecureStrings("key"),
	}}
	store := media.NewFileMediaStore()
	t.Cleanup(func() { store.ReleaseAll("tool:image_generate:test:chat") })
	return NewImageGenerateTool(cfg, maxSize, store), store
}

func imageResponse(image []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(image))
	}
}

func TestImageGenerateStoresFileWithDetectedExtension(t *testing.T) {
	tmp := isolateTempDir(t)
	image := []byte("\x89PNG\r\n\x1a\npayload")
	tool, store := newProxiedImageTool(t, "openai/gpt-image-1", config.DefaultMaxMediaSize, imageResponse(image))

	result := tool.Execute(tools.WithToolContext(context.Background(), "test", "chat"), map[string]any{"prompt": "cat"})
	if result.IsError || len(result.Media) != 1 {
		t.Fatalf("result error = %t, media refs = %d, output = %s", result.IsError, len(result.Media), result.ForLLM)
	}
	path, meta, err := store.ResolveWithMeta(result.Media[0])
	if err != nil {
		t.Fatalf("resolve media: %v", err)
	}
	if filepath.Ext(path) != ".png" || meta.ContentType != "image/png" {
		t.Fatalf("path = %q, content type = %q, want a .png image/png file", path, meta.ContentType)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, image) {
		t.Fatalf("stored file = %q, error = %v, want the generated image", got, err)
	}
	if files := generatedFiles(t, tmp); len(files) != 1 || files[0] != path {
		t.Fatalf("temp files = %v, want only %q (no leftover before the rename)", files, path)
	}
}

func TestImageGenerateLeavesNoTempFileOnFailure(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n")
	tests := []struct {
		name    string
		model   string
		maxSize int
		handler http.HandlerFunc
		wantErr string
	}{
		{
			name:    "unsupported image format",
			model:   "openai/gpt-image-1",
			maxSize: config.DefaultMaxMediaSize,
			handler: imageResponse([]byte("definitely not an image")),
			wantErr: "unsupported image format",
		},
		{
			name:    "image larger than the media limit",
			model:   "openai/gpt-image-1",
			maxSize: 16,
			handler: imageResponse(append(png, bytes.Repeat([]byte("x"), 4096)...)),
			wantErr: "exceeds the configured media size limit",
		},
		{
			name:    "provider error response",
			model:   "openai/gpt-image-1",
			maxSize: config.DefaultMaxMediaSize,
			handler: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusBadGateway) },
			wantErr: "HTTP 502",
		},
		{
			name:    "unsupported provider",
			model:   "anthropic/claude-sonnet",
			maxSize: config.DefaultMaxMediaSize,
			handler: imageResponse(png),
			wantErr: "not implemented for provider",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := isolateTempDir(t)
			tool, _ := newProxiedImageTool(t, tt.model, tt.maxSize, tt.handler)

			result := tool.Execute(
				tools.WithToolContext(context.Background(), "test", "chat"),
				map[string]any{"prompt": "cat"},
			)
			if !result.IsError || !strings.Contains(result.ForLLM, tt.wantErr) {
				t.Fatalf(
					"result error = %t, output = %q, want an error containing %q",
					result.IsError, result.ForLLM, tt.wantErr,
				)
			}
			if files := generatedFiles(t, tmp); len(files) != 0 {
				t.Fatalf("leftover temp files: %v", files)
			}
		})
	}
}

func TestImageGenerateResponseLimitFollowsMediaLimit(t *testing.T) {
	// A small media limit must shrink the buffered response, not fall back to a
	// large fixed floor.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 8<<20))
	}))
	defer server.Close()

	tool := NewImageGenerateTool(nil, 1024, nil)
	_, err := tool.postJSON(context.Background(), server.Client(), server.URL, "", "", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds the configured media size limit") {
		t.Fatalf("postJSON error = %v, want the media size limit error", err)
	}
}

func TestImageGenerateResponseLimitBoundary(t *testing.T) {
	const maxSize = 1024
	limit := base64.StdEncoding.EncodedLen(maxSize) + imageResponseMargin
	for _, tt := range []struct {
		name    string
		size    int
		wantErr bool
	}{
		{"exactly the limit", limit, false},
		{"one byte over the limit", limit + 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(bytes.Repeat([]byte("x"), tt.size))
			}))
			defer server.Close()

			tool := NewImageGenerateTool(nil, maxSize, nil)
			_, err := tool.postJSON(context.Background(), server.Client(), server.URL, "", "", nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("postJSON error = %v, want error = %t", err, tt.wantErr)
			}
		})
	}
}

func TestValidateGeneratedImage(t *testing.T) {
	if _, _, err := validateGeneratedImage(nil, 0); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty image error = %v, want an empty image error", err)
	}
	contentType, ext, err := validateGeneratedImage([]byte("\x89PNG\r\n\x1a\n"), 8)
	if err != nil || contentType != "image/png" || ext != ".png" {
		t.Fatalf("png = %q %q, error = %v", contentType, ext, err)
	}
	if _, _, err := validateGeneratedImage([]byte("<html></html>"), 13); err == nil {
		t.Fatal("expected an unsupported format error")
	}
}

func TestImageGenerateTruncatesErrorBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"short body kept whole", " rate limited \n", "rate limited"},
		{"long body cut", strings.Repeat("x", 1<<20), strings.Repeat("x", imageErrorBodyLimit) + "..."},
		// 1024 is not a multiple of 3, so the cut lands inside a multi-byte rune.
		{
			"cut inside a rune stays valid UTF-8",
			strings.Repeat("€", 1<<18),
			strings.Repeat("€", imageErrorBodyLimit/3) + "...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			tool := NewImageGenerateTool(nil, config.DefaultMaxMediaSize, nil)
			_, err := tool.postJSON(context.Background(), server.Client(), server.URL, "", "", nil)
			want := "image model returned HTTP 500: " + tt.want
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
			if !utf8.ValidString(err.Error()) {
				t.Fatalf("error is not valid UTF-8: %q", err)
			}
		})
	}
}

func TestImageSink_ZeroLimitRejectsEverything(t *testing.T) {
	// A zero limit must fail closed instead of meaning "unlimited".
	if _, err := newImageSink(io.Discard, 0).Write([]byte("x")); !errors.Is(err, errGeneratedImageTooLarge) {
		t.Fatalf("error = %v, want errGeneratedImageTooLarge", err)
	}
}

func TestImageSink_RejectsOversizedStream(t *testing.T) {
	// downloadImage copies the HTTP body into the sink with io.Copy; whatever the
	// chunking, exactly `limit` bytes may land on disk and the next byte fails.
	var buf bytes.Buffer
	sink := newImageSink(&buf, 4096)
	_, err := io.Copy(sink, iotest.OneByteReader(bytes.NewReader(bytes.Repeat([]byte("x"), 5000))))
	if !errors.Is(err, errGeneratedImageTooLarge) {
		t.Fatalf("error = %v, want errGeneratedImageTooLarge", err)
	}
	if buf.Len() != 4096 || sink.n != 4096 {
		t.Fatalf("wrote %d bytes (sink counted %d), want exactly the 4096-byte limit", buf.Len(), sink.n)
	}
	if len(sink.header) != imageHeaderSniffLen {
		t.Fatalf("header length = %d, want %d", len(sink.header), imageHeaderSniffLen)
	}
}
