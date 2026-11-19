package config

import (
	"encoding/json"
	"testing"
)

func TestExecUnknownCommandPolicyDefaultsAndJSON(t *testing.T) {
	t.Parallel()

	if got := DefaultConfig().Tools.Exec.UnknownCommandPolicy; got != ExecUnknownCommandAsk {
		t.Fatalf("DefaultConfig().Tools.Exec.UnknownCommandPolicy = %q, want %q", got, ExecUnknownCommandAsk)
	}

	var cfg Config
	data := []byte(`{
		"tools": {
			"exec": {
				"unknown_command_policy": "allow"
			}
		}
	}`)
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got := cfg.Tools.Exec.GetUnknownCommandPolicy(); got != ExecUnknownCommandAllow {
		t.Fatalf("UnknownCommandPolicy = %q, want %q", got, ExecUnknownCommandAllow)
	}
}

func TestParseExecUnknownCommandPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", ExecUnknownCommandAsk, false},
		{"ask", ExecUnknownCommandAsk, false},
		{"ALLOW", ExecUnknownCommandAllow, false},
		{" block ", ExecUnknownCommandBlock, false},
		{"review", "", true},
	}
	for _, tt := range tests {
		got, err := ParseExecUnknownCommandPolicy(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseExecUnknownCommandPolicy(%q) error = nil, want error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseExecUnknownCommandPolicy(%q) error = %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseExecUnknownCommandPolicy(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExecApprovalTimeoutDefaultsAndJSON(t *testing.T) {
	t.Parallel()

	if got := DefaultConfig().Tools.Exec.GetApprovalTimeoutSeconds(); got != 300 {
		t.Fatalf("default approval timeout = %d, want 300", got)
	}
	if got := (ExecConfig{}).GetApprovalTimeoutSeconds(); got != DefaultExecApprovalTimeoutSeconds {
		t.Fatalf("zero approval timeout = %d, want the default", got)
	}

	var cfg Config
	if err := json.Unmarshal([]byte(`{"tools":{"exec":{"approval_timeout_seconds":45}}}`), &cfg); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got := cfg.Tools.Exec.GetApprovalTimeoutSeconds(); got != 45 {
		t.Fatalf("approval timeout = %d, want 45", got)
	}
}
