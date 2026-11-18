package httpapi

import "testing"

func TestHTTPProviderForwardsReasoningReplayCapability(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
		apiBase      string
		want         bool
	}{
		{
			name:         "opencode-go strips reasoning",
			providerName: "opencode-go",
			apiBase:      "https://opencode.ai/inference/go/openai/v1",
			want:         false,
		},
		{
			name:         "deepseek replays reasoning",
			providerName: "deepseek",
			apiBase:      "https://api.deepseek.com/v1",
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewHTTPProvider("key", tt.apiBase, "")
			p.SetProviderName(tt.providerName)
			if got := p.ReplaysReasoningContent(); got != tt.want {
				t.Fatalf("ReplaysReasoningContent() = %v, want %v", got, tt.want)
			}
		})
	}
}
