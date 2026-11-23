//go:build linux

package hardwaretools

import (
	"testing"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func TestConfigureUnixSerialPortAppliesFlowControl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		flow      string
		wantCflag uint32
		wantIflag uint32
	}{
		{name: "none", flow: "none"},
		{name: "rtscts", flow: "rtscts", wantCflag: serialCRTSCTS},
		{name: "xonxoff", flow: "xonxoff", wantIflag: unix.IXON | unix.IXOFF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ptmx, tty, err := pty.Open()
			if err != nil {
				t.Fatalf("pty.Open() error = %v", err)
			}
			defer ptmx.Close()
			defer tty.Close()

			fd := int(tty.Fd())
			cfg := serialConfig{
				Port:        tty.Name(),
				Baud:        115200,
				DataBits:    8,
				Parity:      "none",
				StopBits:    1,
				FlowControl: tt.flow,
			}
			if err := configureUnixSerialPort(fd, cfg); err != nil {
				t.Fatalf("configureUnixSerialPort() error = %v", err)
			}
			tio, err := unix.IoctlGetTermios(fd, unix.TCGETS)
			if err != nil {
				t.Fatalf("TCGETS error = %v", err)
			}
			if got := tio.Cflag & serialCRTSCTS; got != tt.wantCflag {
				t.Fatalf("Cflag CRTSCTS = %#x, want %#x", got, tt.wantCflag)
			}
			if got := tio.Iflag & (unix.IXON | unix.IXOFF); got != tt.wantIflag {
				t.Fatalf("Iflag IXON|IXOFF = %#x, want %#x", got, tt.wantIflag)
			}
		})
	}
}
