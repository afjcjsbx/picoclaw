package hardwaretools

import "testing"

func TestUnknownActionResult(t *testing.T) {
	tests := []struct {
		name  string
		valid []string
		want  string
	}{
		{
			name:  "serial",
			valid: []string{"list", "read", "write"},
			want:  "unknown action: fly (valid: list, read, write)",
		},
		{
			name:  "i2c",
			valid: []string{"detect", "scan", "read", "write"},
			want:  "unknown action: fly (valid: detect, scan, read, write)",
		},
		{
			name:  "spi",
			valid: []string{"list", "transfer", "read"},
			want:  "unknown action: fly (valid: list, transfer, read)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := unknownActionResult("fly", tt.valid...)
			if !result.IsError {
				t.Fatalf("IsError = false, want true")
			}
			if result.ForLLM != tt.want {
				t.Fatalf("ForLLM = %q, want %q", result.ForLLM, tt.want)
			}
		})
	}
}
