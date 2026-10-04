package integrationtools

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/tools"
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
		_, _ = fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n")))
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

func TestImageDownloadRejectsNonPublicTargets(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "10.0.0.1", "169.254.169.254", "0.0.0.0", "::1", "fc00::1"} {
		if isPublicImageHost(host) {
			t.Errorf("accepted non-public host %q", host)
		}
		_, err := dialPublicImageHost(context.Background(), "tcp", net.JoinHostPort(host, "443"))
		if err == nil {
			t.Errorf("dialed non-public host %q", host)
		}
	}
	if !isPublicImageHost("8.8.8.8") {
		t.Error("rejected public IP")
	}
}
