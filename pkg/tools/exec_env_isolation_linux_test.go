//go:build linux

package tools

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/isolation"
)

func TestSetPicoclawEnv_OmitsConfigOnLinuxWithIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, home+"/config.json")

	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = true
	isolation.Configure(cfg)
	t.Cleanup(func() { isolation.Configure(config.DefaultConfig()) })

	env := make(map[string]string)
	setPicoclawEnv(env, "")

	if _, ok := env[config.EnvConfig]; ok {
		t.Fatalf("PICOCLAW_CONFIG must be absent on Linux with isolation enabled, got %q", env[config.EnvConfig])
	}
	if env[config.EnvHome] != home {
		t.Fatalf("PICOCLAW_HOME = %q, want %q", env[config.EnvHome], home)
	}
}

func TestSetPicoclawEnv_IncludesConfigWhenIsolationDisabled(t *testing.T) {
	home := t.TempDir()
	configPath := home + "/config.json"
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, configPath)

	cfg := config.DefaultConfig()
	cfg.Isolation.Enabled = false
	isolation.Configure(cfg)
	t.Cleanup(func() { isolation.Configure(config.DefaultConfig()) })

	env := make(map[string]string)
	setPicoclawEnv(env, "")

	if env[config.EnvConfig] != configPath {
		t.Fatalf("PICOCLAW_CONFIG = %q, want %q when isolation disabled", env[config.EnvConfig], configPath)
	}
}
