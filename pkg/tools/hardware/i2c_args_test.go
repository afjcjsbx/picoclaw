package hardwaretools

import "testing"

func TestParseI2CAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    map[string]any
		want    int
		wantErr bool
	}{
		{name: "number", args: map[string]any{"address": float64(0x38)}, want: 0x38},
		{name: "hex string", args: map[string]any{"address": "0x38"}, want: 0x38},
		{name: "decimal string", args: map[string]any{"address": "56"}, want: 0x38},
		{name: "missing", args: map[string]any{}, wantErr: true},
		{name: "reserved", args: map[string]any{"address": float64(0x02)}, wantErr: true},
		{name: "too high", args: map[string]any{"address": float64(0x78)}, wantErr: true},
		{name: "fractional", args: map[string]any{"address": 56.5}, wantErr: true},
		{name: "bad string", args: map[string]any{"address": "abc"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, result := parseI2CAddress(tt.args)
			if tt.wantErr {
				if result == nil || !result.IsError {
					t.Fatalf("parseI2CAddress() result = %#v, want error", result)
				}
				return
			}
			if result != nil {
				t.Fatalf("parseI2CAddress() unexpected error: %s", result.ForLLM)
			}
			if got != tt.want {
				t.Fatalf("parseI2CAddress() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseI2CBus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    map[string]any
		want    string
		wantErr bool
	}{
		{name: "string", args: map[string]any{"bus": "1"}, want: "1"},
		{name: "number", args: map[string]any{"bus": float64(2)}, want: "2"},
		{name: "missing", args: map[string]any{}, wantErr: true},
		{name: "path injection", args: map[string]any{"bus": "1/../2"}, wantErr: true},
		{name: "empty", args: map[string]any{"bus": ""}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, result := parseI2CBus(tt.args)
			if tt.wantErr {
				if result == nil || !result.IsError {
					t.Fatalf("parseI2CBus() result = %#v, want error", result)
				}
				return
			}
			if result != nil {
				t.Fatalf("parseI2CBus() unexpected error: %s", result.ForLLM)
			}
			if got != tt.want {
				t.Fatalf("parseI2CBus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseI2CLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    map[string]any
		want    int
		wantErr bool
	}{
		{name: "default", args: map[string]any{}, want: 1},
		{name: "length", args: map[string]any{"length": float64(6)}, want: 6},
		{name: "count alias", args: map[string]any{"count": float64(6)}, want: 6},
		{name: "count string", args: map[string]any{"count": "6"}, want: 6},
		{name: "both agree", args: map[string]any{"length": float64(6), "count": float64(6)}, want: 6},
		{name: "both disagree", args: map[string]any{"length": float64(6), "count": float64(5)}, wantErr: true},
		{name: "too small", args: map[string]any{"length": float64(0)}, wantErr: true},
		{name: "too large", args: map[string]any{"length": float64(257)}, wantErr: true},
		{name: "fractional", args: map[string]any{"length": 1.5}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseI2CLength(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseI2CLength() = %d, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseI2CLength() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseI2CLength() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestParseI2CRegisterDoesNotSilentlyIgnoreStrings(t *testing.T) {
	t.Parallel()

	value, present, err := parseI2CByteArg(map[string]any{"register": "0x76"}, "register")
	if err != nil {
		t.Fatalf("parseI2CByteArg() error = %v", err)
	}
	if !present || value != 0x76 {
		t.Fatalf("parseI2CByteArg() = (%d, %v), want (118, true)", value, present)
	}

	if _, _, err := parseI2CByteArg(map[string]any{"register": "not-a-byte"}, "register"); err == nil {
		t.Fatal("parseI2CByteArg() accepted an invalid string")
	}
	if _, _, err := parseI2CByteArg(map[string]any{"register": true}, "register"); err == nil {
		t.Fatal("parseI2CByteArg() accepted a bool")
	}
}

func TestParseI2CData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    map[string]any
		want    []byte
		wantErr bool
	}{
		{name: "numbers", args: map[string]any{"data": []any{float64(0xac), float64(0x33), float64(0)}}, want: []byte{0xac, 0x33, 0}},
		{name: "hex strings", args: map[string]any{"data": []any{"0xac", "0x33", "0x00"}}, want: []byte{0xac, 0x33, 0}},
		{name: "byte string", args: map[string]any{"data": "0xac 0x33 0x00"}, want: []byte{0xac, 0x33, 0}},
		{name: "comma string", args: map[string]any{"data": "172,51,0"}, want: []byte{0xac, 0x33, 0}},
		{name: "out of range", args: map[string]any{"data": []any{float64(256)}}, wantErr: true},
		{name: "fractional", args: map[string]any{"data": []any{1.5}}, wantErr: true},
		{name: "wrong type", args: map[string]any{"data": true}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, present, err := parseI2CData(tt.args, "data")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseI2CData() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseI2CData() error = %v", err)
			}
			if !present {
				t.Fatal("parseI2CData() reported absent data")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseI2CData() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("parseI2CData() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}
