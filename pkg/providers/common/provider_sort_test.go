package common

import "testing"

func TestNormalizeProviderSort(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "empty", raw: "", want: ""},
		{name: "price", raw: "price", want: "price"},
		{name: "throughput", raw: "throughput", want: "throughput"},
		{name: "latency", raw: "latency", want: "latency"},
		{name: "trims and lowercases", raw: "  ThroughPut ", want: "throughput"},
		{name: "unknown", raw: "fastest", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeProviderSort(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeProviderSort() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("NormalizeProviderSort() = %q, want %q", got, tt.want)
			}
		})
	}
}
