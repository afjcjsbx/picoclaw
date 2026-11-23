//go:build linux

package hardwaretools

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// serialCRTSCTS is Linux's CRTSCTS bit. x/sys/unix does not export it on
// Linux, but the value is the same across the supported architectures.
const serialCRTSCTS = 0x80000000

func serialGetTermios(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TCGETS)
}

func serialSetSpeed(tio *unix.Termios, baud int) error {
	speed, ok := linuxSerialBaud(baud)
	if !ok {
		return fmt.Errorf("unsupported baud rate on Linux: %d", baud)
	}
	// Linux drivers take the line rate from c_cflag & CBAUD. c_ispeed and
	// c_ospeed alone are not enough (unlike Darwin), and leaving CBAUD at zero
	// asks for B0/hangup or makes the driver fall back to 9600.
	tio.Cflag &^= unix.CBAUD
	tio.Cflag |= speed & unix.CBAUD
	tio.Ispeed = speed
	tio.Ospeed = speed
	return nil
}

func linuxSerialBaud(baud int) (uint32, bool) {
	if speed, err := serialBaudToUnix(baud); err == nil {
		return speed, true
	}
	switch baud {
	case 460800:
		return unix.B460800, true
	case 500000:
		return unix.B500000, true
	case 576000:
		return unix.B576000, true
	case 921600:
		return unix.B921600, true
	case 1000000:
		return unix.B1000000, true
	case 1152000:
		return unix.B1152000, true
	case 1500000:
		return unix.B1500000, true
	case 2000000:
		return unix.B2000000, true
	case 2500000:
		return unix.B2500000, true
	case 3000000:
		return unix.B3000000, true
	case 3500000:
		return unix.B3500000, true
	case 4000000:
		return unix.B4000000, true
	default:
		return 0, false
	}
}

func serialApplyBaud(fd int, baud int) error {
	return nil
}

// serialRS485 matches the Linux struct serial_rs485. The padding keeps the
// struct size stable across kernel versions.
type serialRS485 struct {
	flags              uint32
	delayRTSBeforeSend uint32
	delayRTSAfterSend  uint32
	padding            [5]uint32
}

const serRS485Enabled = 1 << 0

func serialSetRS485(fd int, cfg serialConfig) error {
	if !cfg.RS485 {
		return nil
	}
	value := serialRS485{
		flags:              serRS485Enabled,
		delayRTSBeforeSend: uint32(cfg.RS485DelayBeforeMS),
		delayRTSAfterSend:  uint32(cfg.RS485DelayAfterMS),
	}
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(unix.TIOCSRS485),
		uintptr(unsafe.Pointer(&value)),
	)
	if errno != 0 {
		return fmt.Errorf("TIOCSRS485 failed: %w", errno)
	}
	return nil
}

func validatePlatformSerialConfig(cfg serialConfig) error {
	return nil
}

func validatePlatformSerialBaud(baud int) error {
	if _, ok := linuxSerialBaud(baud); !ok {
		return fmt.Errorf("unsupported baud rate on Linux: %d", baud)
	}
	return nil
}

func serialSetTermios(fd int, tio *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TCSETS, tio)
}
