package tools

import (
	"bytes"
	"strings"
	"testing"
)

func TestBoundedOutputBufferKeepsHeadAndTail(t *testing.T) {
	buf := newBoundedOutputBuffer(4, 4)
	if n, err := buf.Write([]byte("abcdefghijklmnop")); n != 16 || err != nil {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if got, want := buf.Dropped(), int64(8); got != want {
		t.Fatalf("Dropped() = %d, want %d", got, want)
	}
	want := "abcd\n...[8 bytes omitted]...\nmnop"
	if got := buf.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestBoundedOutputBufferAcrossWrites(t *testing.T) {
	buf := newBoundedOutputBuffer(4, 4)
	for _, chunk := range []string{"ab", "cdef", "ghij", "kl"} {
		if _, err := buf.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	want := "abcd\n...[4 bytes omitted]...\nijkl"
	if got := buf.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if got, want := buf.Dropped(), int64(4); got != want {
		t.Fatalf("Dropped() = %d, want %d", got, want)
	}
}

func TestBoundedOutputBufferUnderCapKeepsEverything(t *testing.T) {
	buf := newBoundedOutputBuffer(8, 8)
	if _, err := buf.Write([]byte("short")); err != nil {
		t.Fatal(err)
	}
	if buf.Dropped() != 0 {
		t.Fatalf("Dropped() = %d, want 0", buf.Dropped())
	}
	if got := buf.String(); got != "short" {
		t.Fatalf("String() = %q", got)
	}
}

func TestBoundedOutputBufferEmpty(t *testing.T) {
	buf := newBoundedOutputBuffer(8, 8)
	if !buf.Empty() {
		t.Fatal("new buffer must be empty")
	}
	if got := buf.String(); got != "" {
		t.Fatalf("String() = %q, want empty", got)
	}
	if _, err := buf.Write(nil); err != nil {
		t.Fatal(err)
	}
	if !buf.Empty() {
		t.Fatal("nil write must not mark the buffer non-empty")
	}
}

func BenchmarkBoundedOutputBuffer32MiB(b *testing.B) {
	chunk := bytes.Repeat([]byte("x"), 32<<10)
	writes := (32 << 20) / len(chunk)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := newBoundedOutputBuffer(768<<10, 256<<10)
		for j := 0; j < writes; j++ {
			if _, err := buf.Write(chunk); err != nil {
				b.Fatal(err)
			}
		}
		if !strings.Contains(buf.String(), "bytes omitted") {
			b.Fatal("expected an omission marker")
		}
	}
}
