package index

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors memory files for changes and triggers sync.
type Watcher struct {
	watcher           *fsnotify.Watcher
	workspaceDir      string
	extraPaths        []string
	debounceMs        int
	sessionDebounceMs int
	onSync            func()
	logger            *slog.Logger
	cancel            context.CancelFunc
	wg                sync.WaitGroup
}

// WatcherConfig holds watcher configuration.
type WatcherConfig struct {
	WorkspaceDir      string
	ExtraPaths        []string
	DebounceMs        int
	SessionDebounceMs int
	OnSync            func()
	Logger            *slog.Logger
}

// NewWatcher creates and starts a file system watcher.
func NewWatcher(ctx context.Context, cfg WatcherConfig) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if cfg.DebounceMs <= 0 {
		cfg.DebounceMs = 1500
	}
	if cfg.SessionDebounceMs <= 0 {
		cfg.SessionDebounceMs = 5000
	}

	ctx, cancel := context.WithCancel(ctx)
	w := &Watcher{
		watcher:           fw,
		workspaceDir:      cfg.WorkspaceDir,
		extraPaths:        cfg.ExtraPaths,
		debounceMs:        cfg.DebounceMs,
		sessionDebounceMs: cfg.SessionDebounceMs,
		onSync:            cfg.OnSync,
		logger:            cfg.Logger,
		cancel:            cancel,
	}

	// Add watch paths
	w.addWatchPaths()

	// Start event loop
	w.wg.Add(1)
	go w.eventLoop(ctx)

	return w, nil
}

// addWatchPaths adds the standard watch paths.
func (w *Watcher) addWatchPaths() {
	// Watch workspace root for MEMORY.md / memory.md
	w.addPath(w.workspaceDir)

	// Watch memory/ directory recursively
	memDir := filepath.Join(w.workspaceDir, "memory")
	if dirExists(memDir) {
		w.addPath(memDir)
		filepath.WalkDir(memDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if ignoredDirs[d.Name()] {
					return filepath.SkipDir
				}
				w.addPath(path)
			}
			return nil
		})
	}

	// Watch extra paths
	for _, ep := range w.extraPaths {
		absEP := ep
		if !filepath.IsAbs(ep) {
			absEP = filepath.Join(w.workspaceDir, ep)
		}
		info, err := os.Stat(absEP)
		if err != nil {
			continue
		}
		if info.IsDir() {
			w.addPath(absEP)
		} else {
			w.addPath(filepath.Dir(absEP))
		}
	}
}

func (w *Watcher) addPath(path string) {
	if err := w.watcher.Add(path); err != nil {
		w.logger.Debug("failed to watch path", "path", path, "error", err)
	}
}

// eventLoop processes filesystem events with debouncing.
func (w *Watcher) eventLoop(ctx context.Context) {
	defer w.wg.Done()

	var timer *time.Timer
	debounce := time.Duration(w.debounceMs) * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return

		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			// Filter: only care about .md files in memory paths
			if !isRelevantEvent(event, w.workspaceDir) {
				continue
			}

			w.logger.Debug("file change detected", "path", event.Name, "op", event.Op)

			// Reset debounce timer
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounce, func() {
				if w.onSync != nil {
					w.onSync()
				}
			})

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Warn("watcher error", "error", err)
		}
	}
}

// isRelevantEvent checks if a filesystem event is for a relevant memory file.
func isRelevantEvent(event fsnotify.Event, workspaceDir string) bool {
	// Only care about create, write, remove, rename
	if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
		return false
	}

	name := filepath.Base(event.Name)
	ext := strings.ToLower(filepath.Ext(name))

	// Check for memory files
	if ext == ".md" {
		rel, err := filepath.Rel(workspaceDir, event.Name)
		if err != nil {
			return false
		}
		return IsMemoryPath(rel)
	}

	return false
}

// Close stops the watcher and waits for the event loop to finish.
func (w *Watcher) Close() error {
	w.cancel()
	w.wg.Wait()
	return w.watcher.Close()
}
