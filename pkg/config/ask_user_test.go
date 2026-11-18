package config

import (
	"testing"
	"time"
)

func TestAskUserDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.IsToolEnabled("ask_user") {
		t.Fatal("ask_user should be enabled by default")
	}
	if got := cfg.Tools.AskUser.GetTimeout(); got != 5*time.Minute {
		t.Fatalf("timeout = %v, want 5m", got)
	}
}

func TestAskUserTimeoutOverride(t *testing.T) {
	cfg := ToolsConfig{AskUser: AskUserToolsConfig{TimeoutSeconds: 90}}
	if got := cfg.AskUser.GetTimeout(); got != 90*time.Second {
		t.Fatalf("timeout = %v, want 90s", got)
	}
}

func TestAskUserMaxPromptChars(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.Tools.AskUser.GetMaxPromptChars(); got != DefaultAskUserMaxPromptChars {
		t.Fatalf("default prompt chars = %d, want %d", got, DefaultAskUserMaxPromptChars)
	}
	cfg.Tools.AskUser.MaxPromptChars = 0
	if got := cfg.Tools.AskUser.GetMaxPromptChars(); got != DefaultAskUserMaxPromptChars {
		t.Fatalf("zero prompt chars = %d", got)
	}
	cfg.Tools.AskUser.MaxPromptChars = -5
	if got := cfg.Tools.AskUser.GetMaxPromptChars(); got != DefaultAskUserMaxPromptChars {
		t.Fatalf("negative prompt chars = %d", got)
	}
	cfg.Tools.AskUser.MaxPromptChars = 3000
	if got := cfg.Tools.AskUser.GetMaxPromptChars(); got != 3000 {
		t.Fatalf("override prompt chars = %d", got)
	}
}
