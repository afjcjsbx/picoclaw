//go:build linux || darwin

package hardwaretools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeUnixSerialPathAtAcceptsPlainAndBareNames(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"/dev/ttyUSB0", "ttyUSB0", "ttyACM2", "ttyS0", "serial0"} {
		got, err := normalizeUnixSerialPathAt(input, "/dev")
		if err != nil {
			t.Fatalf("normalizeUnixSerialPathAt(%q) error = %v", input, err)
		}
		if !filepath.IsAbs(got) || filepath.Dir(got) != "/dev" {
			t.Fatalf("normalizeUnixSerialPathAt(%q) = %q, want a direct /dev child", input, got)
		}
	}
}

func TestNormalizeUnixSerialPathAtResolvesByID(t *testing.T) {
	t.Parallel()

	root := canonicalTempDir(t)
	device := filepath.Join(root, "ttyUSB0")
	if err := os.WriteFile(device, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", device, err)
	}
	byID := filepath.Join(root, "serial", "by-id")
	if err := os.MkdirAll(byID, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", byID, err)
	}
	link := filepath.Join(byID, "usb-FTDI_FT232R_USB_UART_A12345678-if00-port0")
	if err := os.Symlink(device, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	got, err := normalizeUnixSerialPathAt(link, root)
	if err != nil {
		t.Fatalf("normalizeUnixSerialPathAt() error = %v", err)
	}
	if got != device {
		t.Fatalf("normalizeUnixSerialPathAt() = %q, want %q", got, device)
	}
}

func TestNormalizeUnixSerialPathAtRejectsNonSerialTarget(t *testing.T) {
	t.Parallel()

	root := canonicalTempDir(t)
	nonSerial := filepath.Join(root, "passwd")
	if err := os.WriteFile(nonSerial, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", nonSerial, err)
	}
	byID := filepath.Join(root, "serial", "by-id")
	if err := os.MkdirAll(byID, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", byID, err)
	}
	link := filepath.Join(byID, "not-a-tty")
	if err := os.Symlink(nonSerial, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, err := normalizeUnixSerialPathAt(link, root); err == nil {
		t.Fatal("normalizeUnixSerialPathAt() accepted a symlink to a non-serial file")
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(temp dir) error = %v", err)
	}
	return dir
}

func TestNormalizeUnixSerialPathAtRejectsTraversalAndOutsideRoot(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		"/dev/serial/by-id/../../etc/passwd",
		"../../etc/passwd",
		"/etc/passwd",
		"/dev/../etc/passwd",
		"/dev/serial/by-id/missing-adapter",
	} {
		if _, err := normalizeUnixSerialPathAt(input, "/dev"); err == nil {
			t.Fatalf("normalizeUnixSerialPathAt(%q) accepted an unsafe path", input)
		}
	}
}
