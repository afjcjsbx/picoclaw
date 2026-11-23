package hardwaretools

import "testing"

func TestParseSPIArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      map[string]any
		wantSpeed uint32
		wantMode  uint8
		wantBits  uint8
		wantErr   bool
	}{
		{
			name:      "defaults",
			args:      map[string]any{"device": "2.0"},
			wantSpeed: 1000000,
			wantBits:  8,
		},
		{
			name:      "explicit values",
			args:      map[string]any{"device": "2.0", "speed": float64(4000000), "mode": float64(3), "bits": float64(8)},
			wantSpeed: 4000000,
			wantMode:  3,
			wantBits:  8,
		},
		{
			name:      "string numbers",
			args:      map[string]any{"device": "2.0", "speed": "1000000", "mode": "1", "bits": "8"},
			wantSpeed: 1000000,
			wantMode:  1,
			wantBits:  8,
		},
		{name: "missing device", args: map[string]any{}, wantErr: true},
		{name: "path injection", args: map[string]any{"device": "2.0/../1"}, wantErr: true},
		{name: "speed zero", args: map[string]any{"device": "2.0", "speed": float64(0)}, wantErr: true},
		{name: "mode too high", args: map[string]any{"device": "2.0", "mode": float64(4)}, wantErr: true},
		{name: "bits 16", args: map[string]any{"device": "2.0", "bits": float64(16)}, wantErr: true},
		{name: "bits string invalid", args: map[string]any{"device": "2.0", "bits": "eight"}, wantErr: true},
		{name: "speed bool", args: map[string]any{"device": "2.0", "speed": true}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, speed, mode, bits, errMsg := parseSPIArgs(tt.args)
			if tt.wantErr {
				if errMsg == "" {
					t.Fatalf("parseSPIArgs() accepted %#v", tt.args)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("parseSPIArgs() error = %q", errMsg)
			}
			if speed != tt.wantSpeed || mode != tt.wantMode || bits != tt.wantBits {
				t.Fatalf("parseSPIArgs() = speed %d mode %d bits %d, want %d/%d/%d",
					speed, mode, bits, tt.wantSpeed, tt.wantMode, tt.wantBits)
			}
		})
	}
}
