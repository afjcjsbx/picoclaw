//go:build darwin

package isolation

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDarwinSeatbeltProfileKeepsInstanceRootReadOnly(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", "echo hello")
	cmd.Dir = workspace

	profile := buildDarwinSeatbeltProfile(root, workspace, cmd, nil)
	if !strings.Contains(profile, `(allow file-write* (subpath "`+workspace+`"))`) {
		t.Fatalf("profile does not allow writes under workspace:\n%s", profile)
	}
	if !strings.Contains(profile, `(allow file-write* (subpath "`+filepath.Join(root, "runtime-user-env")+`"))`) {
		t.Fatalf("profile does not allow writes under runtime-user-env:\n%s", profile)
	}
	if strings.Contains(profile, `(allow file-write* (subpath "`+root+`"))`) {
		t.Fatalf("profile must not allow writes under the instance root:\n%s", profile)
	}
}
