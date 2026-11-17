package instancepath

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	EnvTempMaxBytes           = "PICOCLAW_TMP_MAX_BYTES"
	DefaultTempMaxBytes int64 = 512 << 20
)

var ErrStorageLimit = errors.New("PicoClaw temporary storage limit exceeded")

type storageBudget struct {
	sync.Mutex
	dirs        map[string]struct{}
	used, limit int64
}

var budgets sync.Map // instance tmp root -> *storageBudget, shared across work/media

// File bounds streamed writes across this process's media and working files.
// It intentionally exposes no raw file descriptor that bypasses accounting.
type File struct {
	file   *os.File
	budget *storageBudget
}

func (f *File) Name() string                      { return f.file.Name() }
func (f *File) Close() error                      { return f.file.Close() }
func (f *File) Stat() (os.FileInfo, error)        { return f.file.Stat() }
func (f *File) Sync() error                       { return f.file.Sync() }
func (f *File) WriteString(s string) (int, error) { return f.Write([]byte(s)) }

func (f *File) Write(p []byte) (int, error) {
	b := f.budget
	b.Lock()
	defer b.Unlock()
	if int64(len(p)) > b.limit-b.used {
		// Callers remove completed files themselves. Recount only under pressure
		// to reclaim that space without scanning the tree for every write.
		if err := b.refreshLocked(); err != nil {
			return 0, err
		}
		if int64(len(p)) > b.limit-b.used {
			return 0, fmt.Errorf("%w (%d bytes); cannot write %s", ErrStorageLimit, b.limit, f.Name())
		}
	}
	n, err := f.file.Write(p)
	b.used += int64(n)
	return n, err
}

func (f *File) ReadFrom(src io.Reader) (int64, error) {
	// Do not let io.Copy use os.File.ReadFrom and bypass the bounded Write.
	return io.Copy(struct{ io.Writer }{f}, src)
}

// CreateTempIn creates a bounded file in a run directory returned by TempDir.
// The budget covers this process's work and media runs for the same home.
func CreateTempIn(dir, pattern string) (*File, error) {
	limit := DefaultTempMaxBytes
	if raw := os.Getenv(EnvTempMaxBytes); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("%s must be a positive byte count", EnvTempMaxBytes)
		}
		limit = parsed
	}
	base := filepath.Dir(filepath.Dir(dir))
	value, _ := budgets.LoadOrStore(base, &storageBudget{dirs: make(map[string]struct{})})
	b := value.(*storageBudget)
	b.Lock()
	defer b.Unlock()
	b.dirs[dir] = struct{}{}
	b.limit = limit
	if err := b.refreshLocked(); err != nil {
		return nil, err
	}
	if b.used >= limit {
		return nil, fmt.Errorf("%w (%d bytes) in %s", ErrStorageLimit, limit, base)
	}
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return &File{file: f, budget: b}, nil
}

func (b *storageBudget) refreshLocked() error {
	var total int64
	for dir := range b.dirs {
		err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if entry.IsDir() || entry.Name() == ".lock" || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				if info.Size() > b.limit-total {
					total = b.limit
				} else {
					total += info.Size()
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("measure temporary storage %s: %w", dir, err)
		}
	}
	b.used = total
	return nil
}
