package logger

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	maxLogSize = 2 << 20
	logBackups = 3
)

var (
	rotationConfigMu sync.RWMutex
	rotationMaxBytes int64 = maxLogSize
	rotationBackups        = logBackups
)

type rotatingFile struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	size     int64
	maxBytes int64
	backups  int
}

func newRotatingFile(path string, maxBytes int64, backups int) (*rotatingFile, error) {
	if maxBytes <= 0 || backups < 0 {
		return nil, fmt.Errorf("invalid log rotation limits")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if err := trimLogFile(path, maxBytes); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &rotatingFile{path: path, file: f, size: info.Size(), maxBytes: maxBytes, backups: backups}, nil
}

// SetFileRotation sets the limits used by subsequently opened log files.
func SetFileRotation(maxBytes int64, backups int) error {
	if maxBytes <= 0 || backups < 0 {
		return fmt.Errorf("invalid log rotation limits")
	}
	rotationConfigMu.Lock()
	rotationMaxBytes, rotationBackups = maxBytes, backups
	rotationConfigMu.Unlock()
	return nil
}

func newDefaultRotatingFile(path string) (*rotatingFile, error) {
	maxBytes, backups := fileRotationLimits()
	return newRotatingFile(path, maxBytes, backups)
}

func fileRotationLimits() (int64, int) {
	rotationConfigMu.RLock()
	maxBytes, backups := rotationMaxBytes, rotationBackups
	rotationConfigMu.RUnlock()
	return maxBytes, backups
}

func (w *rotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ensureOpen(); err != nil {
		return 0, err
	}
	if w.size > 0 && int64(len(p)) > w.maxBytes-w.size {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotatingFile) rotate() error {
	file := w.file
	w.file = nil
	if err := file.Close(); err != nil {
		_ = w.ensureOpen()
		return err
	}
	if err := rotateFiles(w.path, w.backups); err != nil {
		_ = w.ensureOpen()
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		_ = w.ensureOpen()
		return err
	}
	w.file = f
	w.size = 0
	return nil
}

func (w *rotatingFile) ensureOpen() error {
	if w.file != nil {
		return nil
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file = f
	w.size = info.Size()
	return nil
}

func (w *rotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func rotateFiles(path string, backups int) error {
	if backups == 0 {
		return os.Remove(path)
	}
	if err := os.Remove(fmt.Sprintf("%s.%d", path, backups)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := backups - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", path, i)
		if err := os.Rename(oldPath, fmt.Sprintf("%s.%d", path, i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(path, path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// trimLogFile bounds an existing oversized log before appending to it.
func trimLogFile(path string, maxBytes int64) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil || info.Size() <= maxBytes {
		f.Close()
		return err
	}
	if _, err = f.Seek(-maxBytes, io.SeekEnd); err != nil {
		f.Close()
		return err
	}
	tail := make([]byte, maxBytes)
	_, readErr := io.ReadFull(f, tail)
	closeErr := f.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if lineEnd := bytes.IndexByte(tail, '\n'); lineEnd >= 0 {
		tail = tail[lineEnd+1:]
	}
	return os.WriteFile(path, tail, info.Mode().Perm())
}

// AppendToFile appends a line-oriented log file using the same bounded policy as gateway logs.
func AppendToFile(path string, data []byte) error {
	appendLogMu.Lock()
	defer appendLogMu.Unlock()
	w, err := newDefaultRotatingFile(path)
	if err != nil {
		return err
	}
	n, writeErr := w.Write(data)
	closeErr := w.Close()
	if writeErr != nil {
		return writeErr
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return closeErr
}

var appendLogMu sync.Mutex
