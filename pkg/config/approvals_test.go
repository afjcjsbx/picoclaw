package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/caarlos0/env/v11"
)

func TestApprovalsConfigEnv(t *testing.T) {
	t.Setenv("PICOCLAW_APPROVALS_MODE", "smart")
	t.Setenv("PICOCLAW_APPROVALS_MODEL", "guardian")
	t.Setenv("PICOCLAW_APPROVALS_SMART_TIMEOUT_SECONDS", "9")

	var cfg ApprovalsConfig
	if err := env.Parse(&cfg); err != nil {
		t.Fatalf("env.Parse: %v", err)
	}
	if cfg.Mode != ApprovalModeSmart ||
		cfg.Model != "guardian" ||
		cfg.SmartTimeoutSeconds != 9 {
		t.Fatalf("env.Parse produced %+v", cfg)
	}
}

func TestDefaultApprovalsModeIsOff(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if cfg.Approvals.Mode != ApprovalModeOff {
		t.Fatalf("DefaultConfig().Approvals.Mode = %q, want %q", cfg.Approvals.Mode, ApprovalModeOff)
	}
}

func TestApprovalsConfigNormalized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       ApprovalsConfig
		wantMode string
		wantSecs int
		wantErr  string
	}{
		{
			name:     "empty defaults to off",
			wantMode: ApprovalModeOff,
			wantSecs: DefaultApprovalSmartTimeoutSeconds,
		},
		{
			name:     "explicit off",
			in:       ApprovalsConfig{Mode: ApprovalModeOff},
			wantMode: ApprovalModeOff,
			wantSecs: DefaultApprovalSmartTimeoutSeconds,
		},
		{
			name: "case and whitespace",
			in: ApprovalsConfig{
				Mode:                " SMART ",
				SmartTimeoutSeconds: 45,
			},
			wantMode: ApprovalModeSmart,
			wantSecs: 45,
		},
		{
			name:     "zero timeout uses default",
			in:       ApprovalsConfig{Mode: ApprovalModeSmart},
			wantMode: ApprovalModeSmart,
			wantSecs: DefaultApprovalSmartTimeoutSeconds,
		},
		{
			name:    "unknown mode",
			in:      ApprovalsConfig{Mode: "yolo"},
			wantErr: "mode",
		},
		{
			name:    "negative timeout",
			in:      ApprovalsConfig{Mode: ApprovalModeSmart, SmartTimeoutSeconds: -1},
			wantErr: "smart_timeout_seconds",
		},
		{
			name: "timeout above maximum",
			in: ApprovalsConfig{
				Mode:                ApprovalModeSmart,
				SmartTimeoutSeconds: MaxApprovalSmartTimeoutSeconds + 1,
			},
			wantErr: "exceeds the maximum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.in.Normalized()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Normalized() error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Normalized() error = %v", err)
			}
			if got.Mode != tt.wantMode {
				t.Errorf("Mode = %q, want %q", got.Mode, tt.wantMode)
			}
			if got.SmartTimeoutSeconds != tt.wantSecs {
				t.Errorf("SmartTimeoutSeconds = %d, want %d", got.SmartTimeoutSeconds, tt.wantSecs)
			}
		})
	}
}

func TestApprovalsConfigJSON(t *testing.T) {
	t.Parallel()

	var cfg Config
	if err := json.Unmarshal([]byte(`{
		"approvals": {
			"mode": "smart",
			"model": "cheap-guardian",
			"smart_timeout_seconds": 12
		}
	}`), &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if cfg.Approvals.Mode != ApprovalModeSmart {
		t.Errorf("Mode = %q, want %q", cfg.Approvals.Mode, ApprovalModeSmart)
	}
	if cfg.Approvals.Model != "cheap-guardian" {
		t.Errorf("Model = %q, want cheap-guardian", cfg.Approvals.Model)
	}
	if cfg.Approvals.SmartTimeoutSeconds != 12 {
		t.Errorf("SmartTimeoutSeconds = %d, want 12", cfg.Approvals.SmartTimeoutSeconds)
	}
}
