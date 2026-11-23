//go:build darwin

package hardwaretools

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	serialCRTSCTS = unix.CRTSCTS
	// IOSSIOSPEED updates the line speed after tcsetattr on macOS. The ioctl
	// number is _IOW('T', 2, speed_t) and speed_t is 64-bit here.
	darwinIOSSIOSPEED = 0x80085402
)

func serialGetTermios(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}

func serialSetSpeed(tio *unix.Termios, baud int) error {
	if speed, err := serialBaudToUnix(baud); err == nil {
		tio.Ispeed = uint64(speed)
		tio.Ospeed = uint64(speed)
		return nil
	}
	// macOS accepts arbitrary numeric rates through IOSSIOSPEED. Set the
	// numeric value in termios first so the initial tcsetattr does not reset
	// the port to a bogus standard rate.
	tio.Ispeed = uint64(baud)
	tio.Ospeed = uint64(baud)
	return nil
}

func serialApplyBaud(fd int, baud int) error {
	if _, err := serialBaudToUnix(baud); err == nil {
		return nil
	}
	speed := uint64(baud)
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(darwinIOSSIOSPEED),
		uintptr(unsafe.Pointer(&speed)),
	)
	if errno != 0 {
		return fmt.Errorf("IOSSIOSPEED %d failed: %w", baud, errno)
	}
	return nil
}

func validatePlatformSerialBaud(baud int) error {
	return nil
}

func validatePlatformSerialConfig(cfg serialConfig) error {
	if cfg.RS485 {
		return fmt.Errorf("RS-485 mode is only supported on Linux")
	}
	return nil
}

func serialSetRS485(fd int, cfg serialConfig) error {
	return nil
}

func serialSetTermios(fd int, tio *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, tio)
}
