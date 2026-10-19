package toolshared

import "testing"

func TestInt64Arg(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		present bool
		want    int64
		wantErr bool
	}{
		{name: "absent uses the default", want: 7},
		{name: "integer float", value: float64(42), present: true, want: 42},
		{name: "int", value: 11, present: true, want: 11},
		{name: "int64", value: int64(12), present: true, want: 12},
		{name: "numeric string", value: "13", present: true, want: 13},
		{name: "fractional float", value: 1.5, present: true, wantErr: true},
		{name: "non-numeric string", value: "abc", present: true, wantErr: true},
		{name: "unsupported type", value: true, present: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{}
			if tt.present {
				args["n"] = tt.value
			}
			got, err := Int64Arg(args, "n", 7)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Int64Arg(%v) error = nil, want an error", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("Int64Arg(%v) error = %v", tt.value, err)
			}
			if got != tt.want {
				t.Errorf("Int64Arg(%v) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}
