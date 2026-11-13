// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSensitiveNameMatchesTokenOnlyAsWholeWord(t *testing.T) {
	sensitive := []string{
		"Token", "BotToken", "APIToken", "access_token", "BOT_TOKEN", "x-auth-token", "refreshToken",
		"ApiKey", "ClientSecret", "PASSWORD", "Authorization", "Set-Cookie", "X-Amz-Signature",
	}
	for _, name := range sensitive {
		if !sensitiveName(name) {
			t.Errorf("sensitiveName(%q) = false, want true", name)
		}
	}

	ordinary := []string{
		"MaxTokensField", "max_tokens_field", "MAX_TOKENS", "Tokens", "tokenizer", "TokenizerName",
		"ModelName", "SESSION_DIR", "auth_method",
	}
	for _, name := range ordinary {
		if sensitiveName(name) {
			t.Errorf("sensitiveName(%q) = true, want false", name)
		}
	}
}

// TestSensitiveValuesSkipsMaxTokensField guards against registering a plain
// setting as a secret: max_tokens_field holds a parameter name such as
// "max_completion_tokens", which would then be masked in every log line and
// chat message.
func TestSensitiveValuesSkipsMaxTokensField(t *testing.T) {
	const modelKey = "oc_sk_test_key_1234567890"
	const settingValue = "max_completion_tokens"
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{
		"version": 3,
		"model_list": [{
			"model_name": "test",
			"model": "openai/gpt-4",
			"api_keys": ["` + modelKey + `"],
			"max_tokens_field": "` + settingValue + `"
		}]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	values := cfg.SensitiveValues()
	if !slices.Contains(values, modelKey) {
		t.Fatalf("model API key missing from SensitiveValues: %v", values)
	}
	if slices.Contains(values, settingValue) {
		t.Fatalf("max_tokens_field value must not be treated as a secret: %v", values)
	}
}
