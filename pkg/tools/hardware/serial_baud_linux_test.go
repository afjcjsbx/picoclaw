//go:build linux

package hardwaretools

import (
	"strconv"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// TestConfigureUnixSerialPortAppliesBaudToCflag pins the Linux termios
// contract: the driver reads the line rate from c_cflag & CBAUD, so setting
// only c_ispeed/c_ospeed would silently leave the port at B0 or 9600.
func TestValidatePlatformSerialBaudLinux(t *testing.T) {
	t.Parallel()

	for _, baud := range []int{9600, 115200, 460800, 921600, 4000000} {
		if err := validatePlatformSerialBaud(baud); err != nil {
			t.Fatalf("validatePlatformSerialBaud(%d) error = %v", baud, err)
		}
	}
	if err := validatePlatformSerialBaud(123456); err == nil {
		t.Fatal("validatePlatformSerialBaud(123456) accepted a non-standard Linux rate")
	}
}

func TestConfigureUnixSerialPortAppliesBaudToCflag(t *testing.T) {
	t.Parallel()

	for _, baud := range []int{9600, 115200, 230400, 460800, 921600} {
		t.Run(strconv.Itoa(baud), func(t *testing.T) {
			ptmx, tty, err := pty.Open()
			if err != nil {
				t.Fatalf("pty.Open() error = %v", err)
			}
			defer ptmx.Close()
			defer tty.Close()

			fd := int(tty.Fd())
			cfg := serialConfig{
				Port:     tty.Name(),
				Baud:     baud,
				DataBits: 8,
				Parity:   "none",
				StopBits: 1,
			}
			if err := configureUnixSerialPort(fd, cfg); err != nil {
				t.Fatalf("configureUnixSerialPort() error = %v", err)
			}

			tio, err := unix.IoctlGetTermios(fd, unix.TCGETS)
			if err != nil {
				t.Fatalf("TCGETS error = %v", err)
			}
			want, ok := linuxSerialBaud(baud)
			if !ok {
				t.Fatalf("linuxSerialBaud(%d) is unsupported", baud)
			}
			if got := tio.Cflag & unix.CBAUD; got != want&unix.CBAUD {
				t.Fatalf("c_cflag CBAUD = %#x, want %#x", got, want&unix.CBAUD)
			}
		})
	}
}
