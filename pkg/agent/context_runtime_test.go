package agent

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestBuildDynamicContextIncludesHostResources(t *testing.T) {
	got := (&ContextBuilder{}).buildDynamicContext("", "", "", "")
	if !strings.Contains(got, fmt.Sprintf("%d logical CPUs", runtime.NumCPU())) {
		t.Errorf("runtime context %q does not include the logical CPU count", got)
	}
	if ram := totalSystemMemory(); ram > 0 {
		want := formatSystemMemory(ram)
		if !strings.Contains(got, want) {
			t.Errorf("runtime context %q does not include %q", got, want)
		}
	}
}

func TestFormatSystemMemory(t *testing.T) {
	for _, tt := range []struct {
		name string
		ram  uint64
		want string
	}{
		{name: "below 1 GiB", ram: 512 << 20, want: "512.0 MiB RAM"},
		{name: "at 1 GiB", ram: 1 << 30, want: "1.0 GiB RAM"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSystemMemory(tt.ram); got != tt.want {
				t.Errorf("formatSystemMemory(%d) = %q, want %q", tt.ram, got, tt.want)
			}
		})
	}
}
