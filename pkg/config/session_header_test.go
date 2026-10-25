package config

import "testing"

func TestModelConfigValidateSessionHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantErr bool
	}{
		{"empty is allowed", "", false},
		{"plain token", "x-opencode-session", false},
		{"token with dots and underscore", "X-Session_ID.1", false},
		{"space", "x session", true},
		{"tab", "x\tsession", true},
		{"vertical tab", "x\vsession", true},
		{"form feed", "x\fsession", true},
		{"carriage return", "x\rsession", true},
		{"line feed", "x\nsession", true},
		{"unicode space", "x\u00a0y", true},
		{"colon", "x:y", true},
		{"slash", "x/y", true},
		{"empty after spaces", "   ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ModelConfig{ModelName: "zen", Model: "openai/gpt-5.4", SessionHeader: tt.header}
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error for session_header %q", tt.header)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil for session_header %q", err, tt.header)
			}
		})
	}
}

func TestExpandMultiKeyModelsPreservesSessionHeader(t *testing.T) {
	models := []*ModelConfig{{
		ModelName:     "zen",
		Provider:      "openai",
		Model:         "gpt-5.4",
		SessionHeader: "x-opencode-session",
	}}
	models[0].APIKeys = SimpleSecureStrings("key-1", "key-2")

	expanded := expandMultiKeyModels(models)
	if len(expanded) != 2 {
		t.Fatalf("expandMultiKeyModels() returned %d entries, want 2", len(expanded))
	}
	for _, m := range expanded {
		if m.SessionHeader != "x-opencode-session" {
			t.Fatalf("entry %q lost session header: got %q", m.ModelName, m.SessionHeader)
		}
	}
}
