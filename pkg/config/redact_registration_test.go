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

func TestSensitiveValuesCollectsConfiguredCredentials(t *testing.T) {
	const modelKey = "plain-model-key-abc123"
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
}

// TestLoadConfigLeavesRedactionSecretsAlone guards the process-wide redaction
// set: web handlers, CLI subcommands and reload attempts that end up rejected
// all call LoadConfig, and none of them may replace the credentials of the
// config that is actually running.
func TestLoadConfigLeavesRedactionSecretsAlone(t *testing.T) {
	const liveSecret = "live-config-secret-9876"
	const loadedKey = "plain-model-key-abc123"
	redact.SetSecrets([]string{liveSecret})
	t.Cleanup(func() { redact.SetSecrets(nil) })

	path := filepath.Join(t.TempDir(), "config.json")
	body := `{
		"version": 3,
		"model_list": [{"model_name": "test", "model": "openai/gpt-4", "api_keys": ["` + loadedKey + `"]}]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := redact.Text("echoed " + liveSecret); strings.Contains(got, liveSecret) {
		t.Fatalf("LoadConfig dropped the live secret: %q", got)
	}
	if got := redact.Text("echoed " + loadedKey); !strings.Contains(got, loadedKey) {
		t.Fatalf("LoadConfig must not register the loaded config's keys, got %q", got)
	}
	if redact.Registered() != 1 {
		t.Fatalf("Registered() = %d, want 1", redact.Registered())
	}
}
