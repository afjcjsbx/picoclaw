//go:build linux

package hardwaretools

import "golang.org/x/sys/unix"

// serialCRTSCTS is Linux's CRTSCTS bit. x/sys/unix does not export it on
// Linux, but the value is the same across the supported architectures.
const serialCRTSCTS = 0x80000000

func serialGetTermios(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TCGETS)
}

func serialSetSpeed(tio *unix.Termios, speed uint32) error {
	// Linux drivers take the line rate from c_cflag & CBAUD. c_ispeed and
	// c_ospeed alone are not enough (unlike Darwin), and leaving CBAUD at zero
	// asks for B0/hangup or makes the driver fall back to 9600.
	tio.Cflag &^= unix.CBAUD
	tio.Cflag |= speed & unix.CBAUD
	tio.Ispeed = speed
	tio.Ospeed = speed
	return nil
}

func serialSetTermios(fd int, tio *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TCSETS, tio)
}
