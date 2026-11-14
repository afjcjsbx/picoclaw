package instancepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type runDirectory struct {
	path string
	lock *os.File // Held until process exit; the OS releases it even after a crash.
}

var runs = struct {
	sync.Mutex
	byBase map[string]runDirectory
}{byBase: make(map[string]runDirectory)}

func processDir(base string) (string, error) {
	runs.Lock()
	defer runs.Unlock()
	if run, ok := runs.byBase[base]; ok {
		if _, err := os.Lstat(run.path); err != nil {
			return "", fmt.Errorf("runtime directory disappeared: %w", err)
		}
		if err := secureDir(run.path); err != nil {
			return "", err
		}
		return run.path, nil
	}
	if err := recoverRuns(base); err != nil {
		return "", err
	}
	run, err := newRun(base)
	if err != nil {
		return "", err
	}
	runs.byBase[base] = run
	return run.path, nil
}

func newRun(base string) (runDirectory, error) {
	// Publish the directory only after holding its lock. A concurrent recovery
	// must never mistake a process that is still starting for an abandoned run.
	dir, err := os.MkdirTemp(base, ".init-")
	if err != nil {
		return runDirectory{}, err
	}
	f, err := openLock(filepath.Join(dir, ".lock"), true)
	if err == nil {
		err = tryLock(f)
	}
	if err != nil {
		if f != nil {
			_ = f.Close()
		}
		_ = os.RemoveAll(dir)
		return runDirectory{}, fmt.Errorf("lock runtime directory %s: %w", dir, err)
	}
	path := filepath.Join(base, "run-"+strings.TrimPrefix(filepath.Base(dir), ".init-"))
	if err := os.Rename(dir, path); err != nil {
		_ = f.Close()
		_ = os.RemoveAll(dir)
		return runDirectory{}, err
	}
	return runDirectory{path: path, lock: f}, nil
}

// recoverRuns removes only published directories whose process lock can be
// acquired. Unknown files, symlinks, and live processes are never swept by age.
func recoverRuns(base string) error {
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "run-") {
			continue
		}
		path := filepath.Join(base, entry.Name())
		lockPath := filepath.Join(path, ".lock")
		info, err := os.Lstat(lockPath)
		if err != nil || !info.Mode().IsRegular() || checkOwner(info) != nil {
			continue
		}
		f, err := openLock(lockPath, false)
		if err != nil {
			continue
		}
		if err := tryLock(f); err != nil {
			_ = f.Close()
			continue
		}
		// Windows cannot unlink an open lock file. Delete the payload while
		// holding the lock, then close it and remove the empty marker directory.
		children, readErr := os.ReadDir(path)
		var removeErr error
		if readErr == nil {
			for _, child := range children {
				if child.Name() != ".lock" {
					if err := os.RemoveAll(filepath.Join(path, child.Name())); err != nil {
						removeErr = err
					}
				}
			}
		}
		_ = f.Close()
		if readErr != nil || removeErr != nil {
			continue
		} // Retain marker for retry.
		_ = os.Remove(lockPath)
		_ = os.Remove(path)
	}
	return nil
}
