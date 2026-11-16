package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecoverGoroutine(t *testing.T) {
	ran := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer recoverGoroutine("test", func() { ran = true })
		panic("boom")
	}()
	<-done
	require.True(t, ran)

	// No panic: the hook must not run.
	ran = false
	func() {
		defer recoverGoroutine("test", func() { ran = true })
	}()
	require.False(t, ran)
}
