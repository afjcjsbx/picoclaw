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
		want := fmt.Sprintf("%.1f GiB RAM", float64(ram)/(1<<30))
		if !strings.Contains(got, want) {
			t.Errorf("runtime context %q does not include %q", got, want)
		}
	}
}
