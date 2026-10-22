// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package tools

import (
	"bytes"
	"strings"
	"testing"
)

func TestHeadTailBuffer_KeepsSmallStreamIntact(t *testing.T) {
	b := newHeadTailBuffer(1024)
	for _, w := range []string{"hello ", "world"} {
		n, err := b.Write([]byte(w))
		if err != nil {
			t.Fatalf("Write(%q): %v", w, err)
		}
		if n != len(w) {
			t.Fatalf("Write(%q) = %d, want %d", w, n, len(w))
		}
	}
	if b.Truncated() {
		t.Fatal("small stream marked truncated")
	}
	if got := string(b.Bytes()); got != "hello world" {
		t.Fatalf("Bytes() = %q, want %q", got, "hello world")
	}
}

func TestHeadTailBuffer_DropsMiddleButKeepsHeadAndTail(t *testing.T) {
	const limit = 1024
	b := newHeadTailBuffer(limit)

	head := bytes.Repeat([]byte("H"), limit/2)
	middle := bytes.Repeat([]byte("M"), 10*limit)
	tail := bytes.Repeat([]byte("T"), limit/2)
	for _, w := range [][]byte{head, middle, tail} {
		if n, err := b.Write(w); err != nil || n != len(w) {
			t.Fatalf("Write(len=%d) = %d, %v; want %d, nil", len(w), n, err, len(w))
		}
	}

	if !b.Truncated() {
		t.Fatal("expected truncation for an oversized stream")
	}
	got := b.Bytes()
	if len(got) > limit {
		t.Fatalf("retained %d bytes, want at most %d", len(got), limit)
	}
	if !bytes.HasPrefix(got, head) {
		t.Fatal("head not preserved")
	}
	if !bytes.HasSuffix(got, tail) {
		t.Fatal("tail not preserved")
	}
	if bytes.Contains(got, []byte("MM")) {
		t.Fatal("middle bytes retained")
	}
}

func TestHeadTailBuffer_KeepsTailAfterExactHeadFill(t *testing.T) {
	const limit = 1024
	b := newHeadTailBuffer(limit)

	if _, err := b.Write(bytes.Repeat([]byte("H"), limit/2)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if b.Truncated() {
		t.Fatal("no bytes were dropped, but the buffer reports truncation")
	}
	if got := string(b.Bytes()); !strings.HasSuffix(got, "abc") {
		t.Fatalf("tail write lost: %q", got)
	}
}

func TestHeadTailBuffer_SlidesTailWindow(t *testing.T) {
	const limit = 1024
	b := newHeadTailBuffer(limit)

	if _, err := b.Write(bytes.Repeat([]byte("H"), limit)); err != nil {
		t.Fatal(err)
	}
	// Both writes land in the tail window; only the last half survives.
	if _, err := b.Write(bytes.Repeat([]byte("1"), limit/2)); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write(bytes.Repeat([]byte("2"), limit/2)); err != nil {
		t.Fatal(err)
	}
	if !b.Truncated() {
		t.Fatal("expected truncation after the tail window slid")
	}
	got := b.Bytes()
	if !bytes.HasSuffix(got, bytes.Repeat([]byte("2"), limit/2)) {
		t.Fatal("expected the newest tail bytes to be kept")
	}
	if bytes.Contains(got, []byte("1")) {
		t.Fatal("expected the older tail bytes to be dropped")
	}
}

func TestExecCaptureLimit(t *testing.T) {
	if got := execCaptureLimit(0); got != 1<<20 {
		t.Errorf("execCaptureLimit(0) = %d, want %d", got, 1<<20)
	}
	if got := execCaptureLimit(50000); got != 1<<20 {
		t.Errorf("execCaptureLimit(50000) = %d, want %d", got, 1<<20)
	}
	if got := execCaptureLimit(2 << 20); got != 4<<20 {
		t.Errorf("execCaptureLimit(2 MiB) = %d, want %d", got, 4<<20)
	}
	if got := execCaptureLimit(1 << 29); got != 1<<30 {
		t.Errorf("execCaptureLimit(512 MiB) = %d, want %d", got, 1<<30)
	}
	if got := execCaptureLimit(1<<29 + 1); got != 1<<30 {
		t.Errorf("execCaptureLimit(512 MiB + 1) = %d, want %d", got, 1<<30)
	}
}
