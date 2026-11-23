//go:build linux || darwin

package hardwaretools

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/creack/pty"
)

func ptySerialConfig(t *testing.T) (*ptyOwner, serialConfig) {
	t.Helper()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	owner := &ptyOwner{master: ptmx, slave: tty}
	t.Cleanup(owner.close)
	return owner, serialConfig{
		Port:        tty.Name(),
		Baud:        115200,
		DataBits:    8,
		Parity:      "none",
		StopBits:    1,
		FlowControl: "none",
	}
}

type ptyOwner struct {
	master *os.File
	slave  *os.File
}

func (p *ptyOwner) close() {
	if p.master != nil {
		p.master.Close()
	}
	if p.slave != nil {
		p.slave.Close()
	}
}

func TestSerialReadUntilStopsAtDelimiter(t *testing.T) {
	t.Parallel()

	owner, cfg := ptySerialConfig(t)
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = owner.master.Write([]byte("OK\r\nNEXT"))
	}()

	data, err := serialReadUntil(context.Background(), cfg, []byte("\r\n"), 64, 2*time.Second)
	if err != nil {
		t.Fatalf("serialReadUntil() error = %v", err)
	}
	if string(data) != "OK\r\n" {
		t.Fatalf("serialReadUntil() = %q, want %q", data, "OK\r\n")
	}
}

func TestSerialReadUntilTimeoutReturnsPartialData(t *testing.T) {
	t.Parallel()

	owner, cfg := ptySerialConfig(t)
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = owner.master.Write([]byte("PARTIAL"))
	}()

	data, err := serialReadUntil(context.Background(), cfg, []byte("\n"), 64, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("serialReadUntil() error = %v", err)
	}
	if string(data) != "PARTIAL" {
		t.Fatalf("serialReadUntil() = %q, want %q", data, "PARTIAL")
	}
}

func TestSerialReadAvailableStopsAfterIdle(t *testing.T) {
	t.Parallel()

	owner, cfg := ptySerialConfig(t)
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = owner.master.Write([]byte("abc"))
	}()

	start := time.Now()
	data, err := serialReadAvailable(context.Background(), cfg, 64, 2*time.Second)
	if err != nil {
		t.Fatalf("serialReadAvailable() error = %v", err)
	}
	if string(data) != "abc" {
		t.Fatalf("serialReadAvailable() = %q, want %q", data, "abc")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("serialReadAvailable() took %v, want an idle stop well before the full timeout", elapsed)
	}
}
