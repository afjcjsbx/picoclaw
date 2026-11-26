package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestHandleFilePreview(t *testing.T) {
	configPath, workspace, outside := setupFilePreviewTestEnv(t, true)
	writeFileForTest(t, filepath.Join(workspace, "note.txt"), "hello preview", 0o600)
	writeFileForTest(t, filepath.Join(workspace, "binary.bin"), string([]byte{0xff, 0xfe}), 0o600)
	writeFileForTest(t, filepath.Join(workspace, "large.txt"), strings.Repeat("a", maxFilePreviewSize+16), 0o600)
	writeFileForTest(t, outside, "outside", 0o600)
	if err := os.Mkdir(filepath.Join(workspace, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerFileRoutes(mux)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantPath   string
		wantText   string
		truncated  bool
	}{
		{
			name:       "workspace file",
			path:       "note.txt",
			wantStatus: http.StatusOK,
			wantPath:   "note.txt",
			wantText:   "hello preview",
		},
		{name: "missing path", wantStatus: http.StatusBadRequest},
		{name: "missing file", path: "missing.txt", wantStatus: http.StatusNotFound},
		{name: "directory", path: "folder", wantStatus: http.StatusNotFound},
		{name: "binary", path: "binary.bin", wantStatus: http.StatusUnsupportedMediaType},
		{name: "traversal", path: "../outside.txt", wantStatus: http.StatusForbidden},
		{name: "absolute outside", path: outside, wantStatus: http.StatusForbidden},
		{name: "large file", path: "large.txt", wantStatus: http.StatusOK, wantPath: "large.txt", truncated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := previewFileRequest(mux, tt.path)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				return
			}
			var got filePreviewResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if got.Path != tt.wantPath {
				t.Fatalf("path = %q, want %q", got.Path, tt.wantPath)
			}
			if got.Truncated != tt.truncated {
				t.Fatalf("truncated = %v, want %v", got.Truncated, tt.truncated)
			}
			if tt.wantText != "" && got.Content != tt.wantText {
				t.Fatalf("content = %q, want %q", got.Content, tt.wantText)
			}
			if tt.truncated && len(got.Content) != maxFilePreviewSize {
				t.Fatalf("len(content) = %d, want %d", len(got.Content), maxFilePreviewSize)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
		})
	}

	t.Run("symlink escape", func(t *testing.T) {
		if err := os.Symlink(outside, filepath.Join(workspace, "escape-link")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		rec := previewFileRequest(mux, "escape-link")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})
}

func TestHandleFilePreviewUnrestrictedAbsolutePath(t *testing.T) {
	configPath, _, outside := setupFilePreviewTestEnv(t, false)
	writeFileForTest(t, outside, "allowed outside", 0o600)
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerFileRoutes(mux)

	rec := previewFileRequest(mux, outside)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got filePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != "allowed outside" {
		t.Fatalf("content = %q, want allowed outside", got.Content)
	}
}

func TestHandleFilePreviewConfigFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	writeFileForTest(t, configPath, "{", 0o600)
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerFileRoutes(mux)
	if rec := previewFileRequest(mux, "note.txt"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
}

func setupFilePreviewTestEnv(t *testing.T, restrict bool) (string, string, string) {
	t.Helper()
	configPath, cleanup := setupOAuthTestEnv(t)
	t.Cleanup(cleanup)
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.Defaults.RestrictToWorkspace = restrict
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	return configPath, workspace, filepath.Join(root, "outside.txt")
}

func previewFileRequest(mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/files/preview?path="+url.QueryEscape(path), nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func writeFileForTest(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
