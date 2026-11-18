package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/config"
	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
)

const maxFilePreviewSize = 384 * 1024

type filePreviewResponse struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

func (h *Handler) registerFileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/files/preview", h.handleFilePreview)
}

func (h *Handler) handleFilePreview(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, "Failed to load config", http.StatusInternalServerError)
		return
	}

	requested := strings.TrimSpace(r.URL.Query().Get("path"))
	if requested == "" {
		http.Error(w, "File path is required", http.StatusBadRequest)
		return
	}
	path := requested
	if strings.HasPrefix(path, "~/") {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			http.Error(w, "Invalid file path", http.StatusBadRequest)
			return
		}
		path = filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(path, "~/")))
	}
	if !cfg.Agents.Defaults.RestrictToWorkspace && !filepath.IsAbs(path) {
		path, err = filepath.Abs(path)
		if err != nil {
			http.Error(w, "Invalid file path", http.StatusBadRequest)
			return
		}
	}
	path, err = fstools.ValidateReadablePathWithAllowPaths(
		path,
		cfg.WorkspacePath(),
		cfg.Agents.Defaults.RestrictToWorkspace,
		nil,
	)
	if err != nil {
		http.Error(w, "File is not available for preview", http.StatusForbidden)
		return
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "Could not read file", http.StatusInternalServerError)
		return
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxFilePreviewSize+1))
	if err != nil {
		http.Error(w, "Could not read file", http.StatusInternalServerError)
		return
	}
	truncated := len(content) > maxFilePreviewSize
	if truncated {
		content = content[:maxFilePreviewSize]
		for attempts := 0; attempts < utf8.UTFMax-1 && len(content) > 0 && !utf8.Valid(content); attempts++ {
			content = content[:len(content)-1]
		}
	}
	if !utf8.Valid(content) {
		http.Error(w, "File is not UTF-8 text", http.StatusUnsupportedMediaType)
		return
	}

	previewPath := path
	if workspace, workspaceErr := filepath.EvalSymlinks(cfg.WorkspacePath()); workspaceErr == nil {
		if relativePath, relErr := filepath.Rel(workspace, path); relErr == nil && filepath.IsLocal(relativePath) {
			previewPath = relativePath
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(filePreviewResponse{
		Path:      filepath.ToSlash(previewPath),
		Content:   string(content),
		Truncated: truncated,
	})
}
