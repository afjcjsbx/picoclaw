// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/redact"
)

func TestLoadConfigRegistersSensitiveValuesForRedaction(t *testing.T) {
	t.Cleanup(func() { redact.SetSecrets(nil) })

	const modelKey = "oc_sk_test_key_1234567890"
	const headerSecret = "Bearer custom-header-secret"
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{
		"version": 3,
		"model_list": [{
			"model_name": "test",
			"model": "openai/gpt-4",
			"auth_method": "oauth",
			"api_keys": ["` + modelKey + `"],
			"custom_headers": {"Authorization": "` + headerSecret + `"}
		}]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	values := strings.Join(cfg.SensitiveValues(), "\n")
	if !strings.Contains(values, modelKey) {
		t.Fatalf("model API key missing from SensitiveValues")
	}
	if !strings.Contains(values, headerSecret) {
		t.Fatalf("custom header secret missing from SensitiveValues")
	}
	if strings.Contains(values, "oauth") {
		t.Fatalf("ordinary auth_method value must not be registered as a secret")
	}

	for _, secret := range []string{modelKey, headerSecret} {
		got := redact.Text("provider echoed " + secret)
		if strings.Contains(got, secret) {
			t.Fatalf("configured secret was not registered for redaction: %q", got)
		}
		if !strings.Contains(got, redact.Marker) {
			t.Fatalf("expected %s for registered secret: %q", redact.Marker, got)
		}
	}
}
