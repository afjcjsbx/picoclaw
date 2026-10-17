package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigContextFileMaxCharsFromJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"version":3,"agents":{"defaults":{"context_file_max_chars":48000}}}`),
		0o600,
	); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Agents.Defaults.ContextFileMaxChars; got != 48000 {
		t.Fatalf("context_file_max_chars = %d, want 48000", got)
	}
}

func TestLoadConfigContextFileMaxCharsEnvOverride(t *testing.T) {
	t.Setenv("PICOCLAW_AGENTS_DEFAULTS_CONTEXT_FILE_MAX_CHARS", "12345")

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"version":3}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Agents.Defaults.ContextFileMaxChars; got != 12345 {
		t.Fatalf("context_file_max_chars = %d, want 12345", got)
	}
}
