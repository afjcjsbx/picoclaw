package hardwaretools

import "testing"

func TestParseSPISegments(t *testing.T) {
	t.Parallel()

	segments, present, err := parseSPISegments(map[string]any{
		"segments": []any{
			map[string]any{"data": []any{float64(1), float64(2)}},
			map[string]any{"data": []any{float64(3)}, "read_length": float64(4), "delay_usecs": float64(10), "cs_change": true},
			map[string]any{"read_length": float64(2)},
		},
	})
	if err != nil {
		t.Fatalf("parseSPISegments() error = %v", err)
	}
	if !present || len(segments) != 3 {
		t.Fatalf("parseSPISegments() = %#v, present %v", segments, present)
	}
	if string(segments[0].Data) != "\x01\x02" || segments[0].ReadLength != -1 {
		t.Fatalf("segment 0 = %#v", segments[0])
	}
	if string(segments[1].Data) != "\x03" || segments[1].ReadLength != 4 || segments[1].DelayUsecs != 10 || !segments[1].CSChange {
		t.Fatalf("segment 1 = %#v", segments[1])
	}
	if len(segments[2].Data) != 0 || segments[2].ReadLength != 2 {
		t.Fatalf("segment 2 = %#v", segments[2])
	}

	for _, args := range []map[string]any{
		{"segments": []any{}},
		{"segments": "nope"},
		{"segments": []any{map[string]any{}}},
		{"segments": []any{map[string]any{"data": []any{float64(1)}}}, "data": []any{float64(2)}},
		{"segments": []any{map[string]any{"data": []any{float64(1)}}}, "delay_usecs": float64(1)},
	} {
		if _, _, err := parseSPISegments(args); err == nil {
			t.Fatalf("parseSPISegments(%#v) accepted invalid input", args)
		}
	}
}

func TestBuildSPITransfers(t *testing.T) {
	t.Parallel()

	buffers, err := buildSPITransfers([]spiSegment{
		{Data: []byte{1, 2}, ReadLength: -1},
		{Data: []byte{3}, ReadLength: 4, DelayUsecs: 10, CSChange: true},
		{ReadLength: 2},
		{Data: []byte{4}, ReadLength: 0},
	}, 1000000)
	if err != nil {
		t.Fatalf("buildSPITransfers() error = %v", err)
	}
	if len(buffers.transfers) != 4 {
		t.Fatalf("transfers = %d, want 4", len(buffers.transfers))
	}
	if buffers.transfers[0].length != 2 || len(buffers.rxBuffers[0]) != 2 {
		t.Fatalf("segment 0 length = %d, rx = %d", buffers.transfers[0].length, len(buffers.rxBuffers[0]))
	}
	if buffers.transfers[1].length != 4 || len(buffers.rxBuffers[1]) != 4 {
		t.Fatalf("segment 1 length = %d, rx = %d", buffers.transfers[1].length, len(buffers.rxBuffers[1]))
	}
	if buffers.transfers[1].delayUsecs != 10 || buffers.transfers[1].csChange != 1 {
		t.Fatalf("segment 1 delay/cs = %d/%d", buffers.transfers[1].delayUsecs, buffers.transfers[1].csChange)
	}
	if buffers.transfers[2].length != 2 || buffers.transfers[2].txBuf == 0 || len(buffers.rxBuffers[2]) != 2 {
		t.Fatalf("segment 2 = %#v", buffers.transfers[2])
	}
	if buffers.transfers[3].length != 1 || buffers.transfers[3].rxBuf != 0 || len(buffers.rxBuffers[3]) != 0 {
		t.Fatalf("write-only segment = %#v", buffers.transfers[3])
	}

	if got := spiIocMessage(1); got != 0x40206B00 {
		t.Fatalf("spiIocMessage(1) = %#x", got)
	}
	if got := spiIocMessage(2); got != 0x40406B00 {
		t.Fatalf("spiIocMessage(2) = %#x", got)
	}
}

func TestParseSPIReadCount(t *testing.T) {
	t.Parallel()

	if got, err := parseSPIReadCount(map[string]any{"length": float64(4)}); err != nil || got != 4 {
		t.Fatalf("parseSPIReadCount(length) = (%d, %v)", got, err)
	}
	if got, err := parseSPIReadCount(map[string]any{"count": "4"}); err != nil || got != 4 {
		t.Fatalf("parseSPIReadCount(count) = (%d, %v)", got, err)
	}
	if got, err := parseSPIReadCount(map[string]any{"length": float64(4), "count": float64(4)}); err != nil || got != 4 {
		t.Fatalf("parseSPIReadCount(both equal) = (%d, %v)", got, err)
	}
	for _, args := range []map[string]any{
		{},
		{"length": float64(0)},
		{"length": float64(4097)},
		{"length": float64(4), "count": float64(5)},
		{"length": "nope"},
	} {
		if _, err := parseSPIReadCount(args); err == nil {
			t.Fatalf("parseSPIReadCount(%#v) accepted invalid input", args)
		}
	}
}

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
