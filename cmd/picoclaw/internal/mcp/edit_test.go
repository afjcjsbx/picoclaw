package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestValidateEditedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv(config.EnvConfig, path)

	valid := `{"tools":{"mcp":{"enabled":true,"servers":{"x":{"enabled":true,"command":"npx"}}}}}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateEditedConfig(); err != nil {
		t.Fatalf("validateEditedConfig(valid) = %v, want nil", err)
	}

	invalid := `{"tools":{"mcp":{"enabled":true,"servers":{"x":{"enabled":true,"command":"npx","bogus":1}}}}}`
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateEditedConfig(); err == nil {
		t.Fatal("validateEditedConfig(invalid) = nil, want schema error")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := validateEditedConfig(); err == nil {
		t.Fatal("validateEditedConfig(missing file) = nil, want error")
	}
}
