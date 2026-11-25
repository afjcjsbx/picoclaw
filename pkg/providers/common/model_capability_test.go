// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package common

import "testing"

func TestModelDeprecatesTemperature(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  bool
	}{
		{"sonnet 5.5 bare", "claude-sonnet-5-5", true},
		{"sonnet 5.5 dotted", "claude-sonnet-5.5", true},
		{"sonnet 5.5 provider prefix", "anthropic/claude-sonnet-5-5", true},
		{"sonnet 5.5 uppercase", "Anthropic/Claude-Sonnet-5-5", true},
		{"haiku 5.5 bare", "claude-haiku-5-5", true},
		{"haiku 5.5 dotted", "claude-haiku-5.5", true},
		{"opus 5.5 bare", "claude-opus-5-5", true},
		{"opus 5.5 dotted", "claude-opus-5.5", true},
		{"opus 5.5 provider prefix", "anthropic/claude-opus-5-5", true},
		{"sonnet 5", "claude-sonnet-5", true},
		{"haiku 5", "claude-haiku-5", true},
		{"opus 5", "claude-opus-5", true},
		{"sonnet 4.5", "claude-sonnet-4-5-20250929", true},
		{"opus 4.5", "claude-opus-4-5", true},
		{"bedrock opus 4.8 profile", "us.anthropic.claude-opus-4-8-20260514-v1:0", true},
		{"sonnet 4.6", "claude-sonnet-4-6", false},
		{"opus 4.7", "claude-opus-4-7", false},
		{"gpt 5", "gpt-5", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ModelDeprecatesTemperature(tt.model); got != tt.want {
				t.Errorf("ModelDeprecatesTemperature(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}
