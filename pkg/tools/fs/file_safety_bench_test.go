package fstools

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func BenchmarkProtectedReadDir500(b *testing.B) {
	workspace := b.TempDir()
	b.Setenv("PICOCLAW_HOME", b.TempDir())
	b.Setenv("CODEX_HOME", b.TempDir())
	for i := 0; i < 500; i++ {
		name := filepath.Join(workspace, "file-"+strconv.Itoa(i)+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	fs := buildFs(workspace, false, nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries, err := fs.ReadDir(workspace)
		if err != nil {
			b.Fatal(err)
		}
		if len(entries) != 500 {
			b.Fatalf("got %d entries, want 500", len(entries))
		}
	}
}
