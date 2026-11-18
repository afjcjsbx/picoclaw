package skills

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/utils"
)

// MaxReadBytes caps what Catalog.Read returns. Larger files are refused, never
// truncated, so the model cannot act on half an instruction file.
const MaxReadBytes = 256 << 10

// catalogRecheck is how long a catalog is trusted before every SKILL.md is
// stat-ed again (one syscall per skill: about 65 ms for 10,000 skills). Skill
// roots are checked on every call (a few stats), so installs and removals show
// up at once; only in-place edits of a name or description wait. Reads always
// open the file, so instructions are never stale.
const catalogRecheck = 10 * time.Second

var (
	// ErrSkillNotFound covers both unknown and unauthorized skills, so the
	// answer never reveals whether a hidden skill exists.
	ErrSkillNotFound = errors.New("skill not found")
	// ErrAccessRevoked means authorization was withdrawn while the call ran.
	ErrAccessRevoked = errors.New("skill access revoked, operation aborted")
	ErrPathDenied    = errors.New("access denied: path is outside the skill directory")
	ErrTooLarge      = errors.New("skill file too large")
)

// Access reports whether the caller may see a skill. It is evaluated on every
// use (never cached), so a revocation takes effect mid-call. nil allows all.
type Access func(name string) bool

type accessKey struct{}

// WithAccess scopes skill catalog operations on ctx to the skills access allows.
func WithAccess(ctx context.Context, access Access) context.Context {
	return context.WithValue(ctx, accessKey{}, access)
}

func accessFrom(ctx context.Context) Access {
	a, _ := ctx.Value(accessKey{}).(Access)
	return a
}

// Catalog is the searchable list of installed skills: name, description and
// path only. SKILL.md bodies are read on demand by Read and never indexed, so
// memory grows with the metadata, not with the instructions.
//
// The authorization context travels in the ctx of each call (WithAccess); the
// shared snapshot is never filtered in place.
type Catalog struct {
	loader  *SkillsLoader
	recheck time.Duration

	mu      sync.Mutex // serializes refreshes; searches only read an immutable snapshot
	snap    *catalogSnapshot
	checked time.Time
	stamp   [4]int64
}

// catalogSnapshot is immutable. The BM25 index is built on the first search,
// so catalogs that are only read by name never pay for it.
type catalogSnapshot struct {
	entries []SkillInfo
	byName  map[string]int
	once    sync.Once
	index   *utils.BM25Engine[int]
}

func NewCatalog(loader *SkillsLoader) *Catalog {
	return &Catalog{loader: loader, recheck: catalogRecheck}
}

// Resolve returns the canonical name of an installed skill, matched
// case-insensitively, without touching the disk beyond the cheap freshness check.
func (c *Catalog) Resolve(name string) (string, bool) {
	s := c.current()
	i, ok := s.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return "", false
	}
	return s.entries[i].Name, true
}

// Invalidate forces the next call to rescan the skill roots.
func (c *Catalog) Invalidate() {
	c.mu.Lock()
	c.checked = time.Time{}
	c.mu.Unlock()
}

func (c *Catalog) current() *catalogSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := c.loader.stamp()
	if c.snap != nil && stamp == c.stamp && time.Since(c.checked) < c.recheck {
		return c.snap
	}
	list := c.loader.ListSkills() // unchanged files hit the loader's metadata cache
	c.checked, c.stamp = time.Now(), stamp
	if c.snap != nil && slices.Equal(c.snap.entries, list) {
		return c.snap // same metadata: keep the snapshot and its index
	}
	c.snap = newCatalogSnapshot(list)
	return c.snap
}

func newCatalogSnapshot(list []SkillInfo) *catalogSnapshot {
	s := &catalogSnapshot{entries: list, byName: make(map[string]int, len(list))}
	for i := range list {
		s.byName[strings.ToLower(list[i].Name)] = i
	}
	return s
}

func (s *catalogSnapshot) engine() *utils.BM25Engine[int] {
	s.once.Do(func() {
		ids := make([]int, len(s.entries))
		for i := range ids {
			ids[i] = i
		}
		s.index = utils.NewBM25Engine(ids, func(i int) string {
			return searchWords(s.entries[i].Name) + " " + s.entries[i].Description
		}, utils.WithStemming(), utils.WithEnglishStopwords())
	})
	return s.index
}

// searchWords splits identifiers such as "github-manager" into words, since
// the BM25 tokenizer only splits on whitespace.
func searchWords(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}

// Search ranks authorized skills against query (BM25 over name and
// description) and returns up to limit of them. An exact, case-insensitive name
// match always comes first. more reports that further matches were cut off.
func (c *Catalog) Search(ctx context.Context, query string, limit int) (hits []SkillInfo, more bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s, access := c.current(), accessFrom(ctx)
	allowed := func(i int) bool { return access == nil || access(s.entries[i].Name) }

	exact, hasExact := s.byName[strings.ToLower(strings.TrimSpace(query))]
	hasExact = hasExact && allowed(exact)
	if hasExact {
		hits = append(hits, s.entries[exact])
	}

	// With an access filter any candidate may be rejected, so rank them all;
	// otherwise limit+2 covers the exact hit plus the one that proves "more".
	topK := limit + 2
	if access != nil {
		topK = len(s.entries)
	}
	for _, r := range s.engine().Search(searchWords(query), topK) {
		if (hasExact && r.Document == exact) || !allowed(r.Document) {
			continue
		}
		if len(hits) == limit {
			more = true
			break
		}
		hits = append(hits, s.entries[r.Document])
	}

	// Re-verify what is about to be returned: permission or the turn may have
	// changed while the search ran.
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	for i := range hits {
		if access != nil && !access(hits[i].Name) {
			return nil, false, ErrAccessRevoked
		}
	}
	return hits, more, nil
}

// Read returns the complete instructions of an authorized skill: SKILL.md
// without its frontmatter, or the supporting file at relPath inside the skill
// directory. Files over MaxReadBytes are an error, never a truncation.
func (c *Catalog) Read(ctx context.Context, name, relPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s, access := c.current(), accessFrom(ctx)
	i, ok := s.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok || access != nil && !access(s.entries[i].Name) {
		return "", fmt.Errorf("%w: %q (use skills_search to find installed skills)", ErrSkillNotFound, name)
	}
	info := s.entries[i]

	if info.IsPluginSkill() {
		// Plugin files live outside the workspace; only the validated body is served.
		if relPath != "" {
			return "", fmt.Errorf("plugin skills have no supporting files here (resource tool: %q)", info.ResourceTool)
		}
		body, ok := c.loader.LoadSkill(info.Name)
		if !ok {
			return "", fmt.Errorf("%w: %q", ErrSkillNotFound, name)
		}
		return body, nil
	}

	if relPath == "" {
		relPath = "SKILL.md"
	}
	if !filepath.IsLocal(relPath) {
		return "", ErrPathDenied
	}
	relPath = filepath.Clean(relPath)
	// os.Root rejects "..", absolute paths and symlinks that leave the skill
	// directory, and keeps doing so if a link is swapped after this check.
	root, err := os.OpenRoot(filepath.Dir(info.Path))
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrSkillNotFound, name)
	}
	defer root.Close()
	f, err := root.Open(relPath)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("file %q not found in skill %q", relPath, info.Name)
	} else if err != nil {
		return "", ErrPathDenied
	}
	defer f.Close()

	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", relPath)
	}
	if before.Size() > MaxReadBytes {
		return "", fmt.Errorf("%w: %q is %d bytes, the limit is %d", ErrTooLarge, relPath, before.Size(), MaxReadBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxReadBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxReadBytes {
		return "", fmt.Errorf("%w: %q grew past %d bytes while reading", ErrTooLarge, relPath, MaxReadBytes)
	}

	// The skill must be the same file, unmodified, and still authorized.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	after, aerr := f.Stat()
	now, serr := root.Stat(relPath)
	if aerr != nil || serr != nil || !os.SameFile(before, now) ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || int64(len(data)) != after.Size() {
		return "", fmt.Errorf("skill %q changed or was removed while reading, retry", info.Name)
	}
	if access != nil && !access(info.Name) {
		return "", ErrAccessRevoked
	}

	if !utf8.Valid(data) {
		return "", fmt.Errorf("%q is not a text file", relPath)
	}
	if strings.EqualFold(relPath, "SKILL.md") { // case-insensitive filesystems open skill.md too
		_, body := splitFrontmatter(string(data))
		return body, nil
	}
	return string(data), nil
}

// stamp is a cheap change detector for Catalog: the mtime of every skill root
// (adding or removing a skill touches its root) plus the plugin revision.
func (sl *SkillsLoader) stamp() (s [4]int64) {
	for i, root := range sl.SkillRoots() {
		if fi, err := os.Stat(root); err == nil {
			s[i] = fi.ModTime().UnixNano()
		}
	}
	s[3] = int64(sl.pluginRev.Load())
	return s
}
