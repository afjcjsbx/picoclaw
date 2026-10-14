package config

import "testing"

func TestGetVisionMaxImageDimension(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"unset uses default (enabled)", 0, DefaultVisionMaxImageDimension},
		{"explicit cap", 2048, 2048},
		{"negative disables", -1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := AgentDefaults{VisionMaxImageDimension: tt.in}
			if got := d.GetVisionMaxImageDimension(); got != tt.want {
				t.Fatalf("GetVisionMaxImageDimension() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetVisionJPEGQuality(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{0, DefaultVisionJPEGQuality},
		{-5, DefaultVisionJPEGQuality},
		{200, DefaultVisionJPEGQuality},
		{70, 70},
	}
	for _, tt := range tests {
		d := AgentDefaults{VisionJPEGQuality: tt.in}
		if got := d.GetVisionJPEGQuality(); got != tt.want {
			t.Errorf("GetVisionJPEGQuality(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
