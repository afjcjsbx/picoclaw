package gateway

import (
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestPreCheckConfigRequiresLocalCommandExecutionForProcessHooks(t *testing.T) {
	tests := []struct {
		name         string
		allowed      string
		hooksEnabled bool
		hookEnabled  bool
		wantErr      bool
	}{
		{name: "disabled by default", allowed: "false", hooksEnabled: true, hookEnabled: true, wantErr: true},
		{name: "invalid value fails closed", allowed: "invalid", hooksEnabled: true, hookEnabled: true, wantErr: true},
		{name: "explicit opt in", allowed: "true", hooksEnabled: true, hookEnabled: true},
		{name: "disabled hook does not require opt in", allowed: "false", hooksEnabled: true},
		{name: "globally disabled hooks do not require opt in", allowed: "false", hookEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(config.LocalCommandExecutionEnv, tt.allowed)
			cfg := config.DefaultConfig()
			cfg.Hooks.Enabled = tt.hooksEnabled
			cfg.Hooks.Processes = map[string]config.ProcessHookConfig{
				"example": {Enabled: tt.hookEnabled, Command: []string{"example-hook"}},
			}

			err := preCheckConfig(cfg)
			if tt.wantErr {
				if !errors.Is(err, config.ErrLocalCommandExecutionDisabled) {
					t.Fatalf("preCheckConfig() error = %v, want local command execution disabled", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("preCheckConfig() unexpected error: %v", err)
			}
		})
	}
}
