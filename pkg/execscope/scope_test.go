//go:build !windows

package execscope

import (
	"reflect"
	"testing"
)

func TestDescendantsFromParentMapDeepestFirst(t *testing.T) {
	parent := map[int]int{
		10: 1, // root
		20: 10,
		30: 10,
		40: 20,
	}

	got := descendantsFromParentMap(10, parent)
	want := []int{40, 20, 30}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("descendantsFromParentMap() = %v, want %v", got, want)
	}
}

func TestDescendantsFromParentMapHandlesCycles(t *testing.T) {
	parent := map[int]int{
		10: 20,
		20: 10,
	}
	got := descendantsFromParentMap(10, parent)
	if len(got) != 1 || got[0] != 20 {
		t.Fatalf("descendantsFromParentMap() = %v, want [20]", got)
	}
}
