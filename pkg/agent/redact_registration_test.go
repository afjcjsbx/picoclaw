// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/redact"
)

func testConfigWithVoiceKey(t *testing.T, key string) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	cfg.Voice.ElevenLabsAPIKey = key
	return cfg
}

func masked(value string) bool {
	return !strings.Contains(redact.Text("provider echoed "+value), value)
}

func TestNewAgentLoopRegistersConfigSecretsForRedaction(t *testing.T) {
	const staleKey = "stale-secret-value-999"
	const liveKey = "voice-key-plain-123456"
	redact.SetSecrets([]string{staleKey})
	t.Cleanup(func() { redact.SetSecrets(nil) })

	al := NewAgentLoop(testConfigWithVoiceKey(t, liveKey), bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	if !masked(liveKey) {
		t.Fatal("credential of the live config was not registered for redaction")
	}
	if masked(staleKey) {
		t.Fatal("secret of a previous config must be dropped when a new config goes live")
	}
}

func TestReloadProviderAndConfigSwapsRedactionSecrets(t *testing.T) {
	const oldKey = "voice-key-old-123456"
	const newKey = "voice-key-new-654321"
	t.Cleanup(func() { redact.SetSecrets(nil) })

	al := NewAgentLoop(testConfigWithVoiceKey(t, oldKey), bus.NewMessageBus(), &mockProvider{})
	defer al.Close()
	if !masked(oldKey) {
		t.Fatal("initial credential was not registered for redaction")
	}

	reloaded := testConfigWithVoiceKey(t, newKey)
	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, reloaded); err != nil {
		t.Fatalf("ReloadProviderAndConfig() error = %v", err)
	}

	if !masked(newKey) {
		t.Fatal("rotated credential was not registered after reload")
	}
	if masked(oldKey) {
		t.Fatal("retired credential must be dropped after reload")
	}
}
