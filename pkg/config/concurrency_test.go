package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadConfigConcurrentDirectoriesResolveOwnCredentials(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	pathA := writeFileRefConfig(t, dirA, "secret-a")
	pathB := writeFileRefConfig(t, dirB, "secret-b")

	const iterations = 40
	var wg sync.WaitGroup
	errs := make(chan error, iterations*2)
	for range iterations {
		wg.Add(2)
		go func() {
			defer wg.Done()
			cfg, err := LoadConfig(pathA)
			if err != nil {
				errs <- err
				return
			}
			if got := cfg.ModelList[0].APIKey(); got != "secret-a" {
				errs <- fmt.Errorf("config A resolved credential %q, want secret-a", got)
			}
		}()
		go func() {
			defer wg.Done()
			cfg, err := LoadConfig(pathB)
			if err != nil {
				errs <- err
				return
			}
			if got := cfg.ModelList[0].APIKey(); got != "secret-b" {
				errs <- fmt.Errorf("config B resolved credential %q, want secret-b", got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestSaveConfigConcurrentWritesRemainValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := DefaultConfig()
			cfg.Gateway.Port = 20000 + i
			errs <- SaveConfig(path, cfg)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("SaveConfig() error = %v", err)
		}
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() after concurrent writes error = %v", err)
	}
	if cfg.Gateway.Port < 20000 || cfg.Gateway.Port >= 20000+writers {
		t.Fatalf("final gateway port = %d, want one of the written values", cfg.Gateway.Port)
	}
}

func writeFileRefConfig(t *testing.T, dir, secret string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.ModelList = []*ModelConfig{{
		ModelName: "file-ref-model",
		Model:     "openai/test-model",
	}}
	cfg.ModelList[0].SetAPIKey("file://secret.txt")
	cfg.Agents.Defaults.ModelName = "file-ref-model"
	path := filepath.Join(dir, "config.json")
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	return path
}
