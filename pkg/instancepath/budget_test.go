package instancepath

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestBudgetSharedAcrossMediaAndWork(t *testing.T) {
	t.Setenv(EnvHome, t.TempDir())
	t.Setenv(EnvTempMaxBytes, "12")
	work, err := CreateTemp("download-*")
	if err != nil {
		t.Fatal(err)
	}
	defer work.Close()
	if _, err := io.Copy(work, strings.NewReader("12345678")); err != nil {
		t.Fatal(err)
	}
	dir, err := TempDir("media")
	if err != nil {
		t.Fatal(err)
	}
	image, err := CreateTempIn(dir, "photo-*")
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	if _, err := io.Copy(image, strings.NewReader("12345")); !errors.Is(err, ErrStorageLimit) {
		t.Fatalf("budget bypassed: %v", err)
	}
	if err := work.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(work.Name()); err != nil {
		t.Fatal(err)
	}
	if _, err := image.WriteString("12345"); err != nil {
		t.Fatalf("deleted space not reclaimed: %v", err)
	}
}

func TestBudgetConcurrentWriters(t *testing.T) {
	t.Setenv(EnvHome, t.TempDir())
	t.Setenv(EnvTempMaxBytes, "10")
	a, err := CreateTemp("a-*")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateTemp("b-*")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	defer b.Close()
	var wg sync.WaitGroup
	for _, f := range []*File{a, b} {
		wg.Go(func() {
			_, err := f.WriteString("12345678")
			if err != nil && !errors.Is(err, ErrStorageLimit) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	ai, err := a.Stat()
	if err != nil {
		t.Fatal(err)
	}
	bi, err := b.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if ai.Size()+bi.Size() != 8 {
		t.Fatalf("concurrent writes exceeded budget: %d", ai.Size()+bi.Size())
	}
}

func TestBudgetRejectsInvalidLimit(t *testing.T) {
	t.Setenv(EnvHome, t.TempDir())
	for _, value := range []string{"-1", "0", "invalid"} {
		t.Setenv(EnvTempMaxBytes, value)
		if _, err := CreateTemp("test-*"); err == nil {
			t.Fatalf("accepted limit %q", value)
		}
	}
}
