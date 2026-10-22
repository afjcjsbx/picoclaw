package toolshared

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sipeed/picoclaw/pkg/media"
)

// fakeMediaStore records the Store call and, optionally, fails it.
type fakeMediaStore struct {
	fail bool

	path  string
	data  []byte
	meta  media.MediaMeta
	scope string
}

func (f *fakeMediaStore) Store(localPath string, meta media.MediaMeta, scope string) (string, error) {
	f.path = localPath
	if f.fail {
		return "", errors.New("store unavailable")
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		return "", err
	}
	f.data = data
	f.meta = meta
	f.scope = scope
	return "media://one", nil
}

func (f *fakeMediaStore) Resolve(string) (string, error) { return "", nil }

func (f *fakeMediaStore) ResolveWithMeta(string) (string, media.MediaMeta, error) {
	return "", media.MediaMeta{}, nil
}

func (f *fakeMediaStore) ReleaseAll(string) error { return nil }

func TestStoreTempMediaRegistersFile(t *testing.T) {
	store := &fakeMediaStore{}

	ref, err := StoreTempMedia(StoreTempMediaInput{
		Store:      store,
		Data:       []byte("png-bytes"),
		MIMEType:   "image/png",
		TempPrefix: "test-store-",
		Filename:   "shot.png",
		Source:     "tool:test",
		Scope:      "tool:test:1",
	})
	if err != nil {
		t.Fatalf("StoreTempMedia() error: %v", err)
	}

	if ref != "media://one" {
		t.Errorf("ref = %q, want media://one", ref)
	}
	if string(store.data) != "png-bytes" {
		t.Errorf("stored bytes = %q, want png-bytes", store.data)
	}
	if filepath.Ext(store.path) != ".png" {
		t.Errorf("temp file %q does not carry the MIME extension", store.path)
	}
	if store.meta.Filename != "shot.png" || store.meta.ContentType != "image/png" || store.meta.Source != "tool:test" {
		t.Errorf("meta = %+v", store.meta)
	}
	if store.scope != "tool:test:1" {
		t.Errorf("scope = %q, want tool:test:1", store.scope)
	}
}

func TestStoreTempMediaRemovesFileWhenRegistrationFails(t *testing.T) {
	store := &fakeMediaStore{fail: true}

	_, err := StoreTempMedia(StoreTempMediaInput{
		Store:      store,
		Data:       []byte("png-bytes"),
		MIMEType:   "image/png",
		TempPrefix: "test-store-",
		Filename:   "shot.png",
		Source:     "tool:test",
		Scope:      "tool:test:1",
	})
	if !errors.Is(err, ErrMediaStoreRegistration) {
		t.Fatalf("error = %v, want it to wrap ErrMediaStoreRegistration", err)
	}

	if _, statErr := os.Stat(store.path); !os.IsNotExist(statErr) {
		t.Errorf("temporary file %q survived a failed registration (stat error: %v)", store.path, statErr)
	}
}
