package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageContainment(t *testing.T) {
	root, err := canonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(filepath.Join(root, "inside"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"escape", outside, "../secret"} {
		if _, err := ReadPackageFile(root, path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if data, err := ReadPackageFile(root, "link"); err != nil || string(data) != "safe" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if _, err := ReadPackageFile(root, "."); err == nil {
		t.Fatal("accepted directory")
	}
}

func TestEnvironmentAndCWD(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	data, _ := canonicalRoot(t.TempDir())
	t.Setenv("PICOCLAW_PRIVATE_TEST_TOKEN", "secret")
	value := Expand("${PLUGIN_ROOT}/${PLUGIN_DATA}/${HOME}", "/root/${PLUGIN_DATA}", "/data")
	if value != "/root/${PLUGIN_DATA}//data/${HOME}" {
		t.Fatal(value)
	}
	env := strings.Join(Environment(root, data, map[string]string{"VALUE": "${PLUGIN_ROOT}", "PLUGIN_ROOT": "bad"}), "\n")
	if strings.Contains(env, "PRIVATE_TEST_TOKEN") || !strings.Contains(env, "PLUGIN_ROOT="+root) || !strings.Contains(env, "VALUE="+root) {
		t.Fatal(env)
	}
	for _, value := range []string{"./", "${PLUGIN_ROOT}", "${PLUGIN_DATA}"} {
		if _, err := workingDirectory(root, data, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []string{"/tmp", "../", "${HOME}", "${PLUGIN_ROOT}/../", "${PLUGIN_DATA}/../"} {
		if _, err := workingDirectory(root, data, value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
