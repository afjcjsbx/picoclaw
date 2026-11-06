package agent

import (
	"fmt"
	"testing"
)

func TestIsVisionUnsupportedError(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"nil", "", false},
		{"openrouter", "no endpoints found that support image input", true},
		{"common", "does not support images", true},
		{"common input", "image input is not supported", true},
		{"image_url invalid", "invalid request: unsupported content type: image_url", true},
		{"unknown variant", "unknown variant `image_url`, expected one of", true},
		// DeepSeek / strict providers.
		{"deepseek uploaded", "You have uploaded an unsupported image. Please make sure your image is valid", true},
		{"deepseek unsupported image", "messages.5: unsupported image", true},
		{"workbuddy replace", "Please start a new conversation, replace the image, and try again.", true},
		{"non-image error", "rate limit exceeded", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVisionUnsupportedError(fmt.Errorf("%s", tt.msg)); got != tt.want {
				t.Errorf("isVisionUnsupportedError(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}
