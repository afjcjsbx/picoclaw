package hardwaretools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// I2C ioctl constants from Linux kernel headers (<linux/i2c-dev.h>, <linux/i2c.h>)
const (
	i2cSlave = 0x0703 // Set slave address (fails if in use by driver)
	i2cFuncs = 0x0705 // Query adapter functionality bitmask
	i2cRdwr  = 0x0707 // Combined read/write transactions
	i2cSmbus = 0x0720 // Perform SMBus transaction

	// i2c_msg flags
	i2cMsgRead = 0x0001

	// I2C_RDWR ioctl payload: struct i2c_rdwr_ioctl_data
	i2cRdwrMaxMessages = 42 // kernel I2C_RDWR_IOCTL_MAX_MSGS

	// I2C_FUNC capability bits
	i2cFuncSmbusQuick    = 0x00010000
	i2cFuncSmbusReadByte = 0x00020000

	// SMBus transaction types
	i2cSmbusRead  = 0
	i2cSmbusWrite = 1

	// SMBus protocol sizes
	i2cSmbusQuick = 0
	i2cSmbusByte  = 1
)

// i2cSmbusData matches the kernel union i2c_smbus_data (34 bytes max).
// For quick and byte transactions only the first byte is used (if at all).
type i2cSmbusData [34]byte

// i2cSmbusArgs matches the kernel struct i2c_smbus_ioctl_data.
type i2cSmbusArgs struct {
	readWrite uint8
	command   uint8
	size      uint32
	data      *i2cSmbusData
}

// i2cMsg matches the kernel struct i2c_msg.
type i2cMsg struct {
	addr  uint16
	flags uint16
	len   uint16
	buf   *byte
}

// i2cRdwrData matches the kernel struct i2c_rdwr_ioctl_data.
type i2cRdwrData struct {
	msgs  *i2cMsg
	nmsgs uint32
}

// i2cTransfer executes one or more messages with I2C_RDWR. Consecutive
// messages are separated by a repeated START, which is what register-read
// sensors and SMBus-style devices expect.
func i2cTransfer(fd int, msgs []i2cMsg) error {
	if len(msgs) == 0 {
		return nil
	}
	if len(msgs) > i2cRdwrMaxMessages {
		return fmt.Errorf("too many I2C messages: %d (max %d)", len(msgs), i2cRdwrMaxMessages)
	}
	data := i2cRdwrData{msgs: &msgs[0], nmsgs: uint32(len(msgs))}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		i2cRdwr,
		uintptr(unsafe.Pointer(&data)),
	)
	runtime.KeepAlive(msgs)
	if errno != 0 {
		return fmt.Errorf("I2C_RDWR failed: %w", errno)
	}
	return nil
}

func i2cSetSlave(fd, addr int) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), i2cSlave, uintptr(addr))
	if errno != 0 {
		return errno
	}
	return nil
}

func i2cRdwrUnsupported(err error) bool {
	return errors.Is(err, syscall.ENOTTY) || errors.Is(err, syscall.EOPNOTSUPP)
}

func i2cWriteBytes(fd, addr int, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	msgs := []i2cMsg{{
		addr: uint16(addr),
		len:  uint16(len(data)),
		buf:  &data[0],
	}}
	err := i2cTransfer(fd, msgs)
	if err == nil {
		return nil
	}
	if !i2cRdwrUnsupported(err) {
		return err
	}
	// Older or SMBus-only adapters do not implement I2C_RDWR. A single write
	// is still possible through the classic I2C_SLAVE + write path.
	if slaveErr := i2cSetSlave(fd, addr); slaveErr != nil {
		return slaveErr
	}
	written, err := syscall.Write(fd, data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func i2cReadBytes(fd, addr, length int) ([]byte, error) {
	buf := make([]byte, length)
	msgs := []i2cMsg{{
		addr:  uint16(addr),
		flags: i2cMsgRead,
		len:   uint16(length),
		buf:   &buf[0],
	}}
	err := i2cTransfer(fd, msgs)
	if err == nil {
		return buf, nil
	}
	if !i2cRdwrUnsupported(err) {
		return nil, err
	}
	if slaveErr := i2cSetSlave(fd, addr); slaveErr != nil {
		return nil, slaveErr
	}
	n, err := syscall.Read(fd, buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func i2cCombinedRead(fd, addr int, prefix []byte, length int) ([]byte, error) {
	buf := make([]byte, length)
	msgs := make([]i2cMsg, 0, 2)
	if len(prefix) > 0 {
		msgs = append(msgs, i2cMsg{
			addr: uint16(addr),
			len:  uint16(len(prefix)),
			buf:  &prefix[0],
		})
	}
	msgs = append(msgs, i2cMsg{
		addr:  uint16(addr),
		flags: i2cMsgRead,
		len:   uint16(length),
		buf:   &buf[0],
	})
	if err := i2cTransfer(fd, msgs); err != nil {
		return nil, err
	}
	return buf, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func formatI2CBytes(data []byte) ([]string, []int) {
	hexBytes := make([]string, len(data))
	intBytes := make([]int, len(data))
	for i, b := range data {
		hexBytes[i] = fmt.Sprintf("0x%02x", b)
		intBytes[i] = int(b)
	}
	return hexBytes, intBytes
}

// smbusProbe performs a single SMBus probe at the given address.
// Uses SMBus Quick Write (safest) or falls back to SMBus Read Byte for
// EEPROM address ranges where quick write can corrupt AT24RF08 chips.
// This matches i2cdetect's MODE_AUTO behavior.
func smbusProbe(fd int, addr int, hasQuick bool) bool {
	// EEPROM ranges: use read byte (quick write can corrupt AT24RF08)
	useReadByte := (addr >= 0x30 && addr <= 0x37) || (addr >= 0x50 && addr <= 0x5F)

	if !useReadByte && hasQuick {
		// SMBus Quick Write: [START] [ADDR|W] [ACK/NACK] [STOP]
		// Safest probe — no data transferred
		args := i2cSmbusArgs{
			readWrite: i2cSmbusWrite,
			command:   0,
			size:      i2cSmbusQuick,
			data:      nil,
		}
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), i2cSmbus, uintptr(unsafe.Pointer(&args)))
		return errno == 0
	}

	// SMBus Read Byte: [START] [ADDR|R] [ACK/NACK] [DATA] [STOP]
	var data i2cSmbusData
	args := i2cSmbusArgs{
		readWrite: i2cSmbusRead,
		command:   0,
		size:      i2cSmbusByte,
		data:      &data,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), i2cSmbus, uintptr(unsafe.Pointer(&args)))
	return errno == 0
}

// scan probes valid 7-bit addresses on a bus for connected devices.
// Uses the same hybrid probe strategy as i2cdetect's MODE_AUTO:
// SMBus Quick Write for most addresses, SMBus Read Byte for EEPROM ranges.
func (t *I2CTool) scan(ctx context.Context, args map[string]any) *ToolResult {
	if err := ctx.Err(); err != nil {
		return ErrorResult(err.Error())
	}
	bus, errResult := parseI2CBus(args)
	if errResult != nil {
		return errResult
	}
	if result := t.writePolicy().checkBus(bus, bus+":"); result != nil {
		return result
	}
	unlock := lockI2CBus(bus)
	defer unlock()

	devPath := fmt.Sprintf("/dev/i2c-%s", bus)
	fd, err := syscall.Open(devPath, syscall.O_RDWR, 0)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to open %s: %v (check permissions and i2c-dev module)", devPath, err))
	}
	defer syscall.Close(fd)

	// Query adapter capabilities to determine available probe methods.
	// I2C_FUNCS writes an unsigned long, which is word-sized on Linux.
	var funcs uintptr
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), i2cFuncs, uintptr(unsafe.Pointer(&funcs)))
	if errno != 0 {
		return ErrorResult(fmt.Sprintf("failed to query I2C adapter capabilities on %s: %v", devPath, errno))
	}

	hasQuick := funcs&i2cFuncSmbusQuick != 0
	hasReadByte := funcs&i2cFuncSmbusReadByte != 0

	if !hasQuick && !hasReadByte {
		return ErrorResult(
			fmt.Sprintf("I2C adapter %s supports neither SMBus Quick nor Read Byte — cannot probe safely", devPath),
		)
	}

	type deviceEntry struct {
		Address string `json:"address"`
		Status  string `json:"status,omitempty"`
	}

	var found []deviceEntry
	// Scan 0x08-0x77, skipping I2C reserved addresses 0x00-0x07.
	const scanContextInterval = 8
	for addr := 0x08; addr <= 0x77; addr++ {
		if (addr-0x08)%scanContextInterval == 0 {
			if err := ctx.Err(); err != nil {
				return ErrorResult(fmt.Sprintf("scan canceled after 0x%02x: %v", addr, err))
			}
		}
		// Set slave address — EBUSY means a kernel driver owns this address
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), i2cSlave, uintptr(addr))
		if errno != 0 {
			if errno == syscall.EBUSY {
				found = append(found, deviceEntry{
					Address: fmt.Sprintf("0x%02x", addr),
					Status:  "busy (in use by kernel driver)",
				})
			}
			continue
		}

		if smbusProbe(fd, addr, hasQuick) {
			found = append(found, deviceEntry{
				Address: fmt.Sprintf("0x%02x", addr),
			})
		}
	}

	if len(found) == 0 {
		return SilentResult(fmt.Sprintf("No devices found on %s. Check wiring and pull-up resistors.", devPath))
	}

	result, _ := json.MarshalIndent(map[string]any{
		"bus":     devPath,
		"devices": found,
		"count":   len(found),
	}, "", "  ")
	return SilentResult(fmt.Sprintf("Scan of %s:\n%s", devPath, string(result)))
}

// readDevice reads bytes from an I2C device, optionally after a command or
// register write. repeated_start keeps command and read in one bus transaction;
// delay_ms splits them for devices that need conversion time between phases.
func (t *I2CTool) readDevice(ctx context.Context, args map[string]any) *ToolResult {
	if err := ctx.Err(); err != nil {
		return ErrorResult(err.Error())
	}
	bus, errResult := parseI2CBus(args)
	if errResult != nil {
		return errResult
	}
	unlock := lockI2CBus(bus)
	defer unlock()

	addr, errResult := parseI2CAddress(args)
	if errResult != nil {
		return errResult
	}
	if result := t.writePolicy().checkTarget(i2cTarget(bus, addr)); result != nil {
		return result
	}

	length, err := parseI2CLength(args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	register, hasRegister, err := parseI2CByteArg(args, "register")
	if err != nil {
		return ErrorResult(err.Error())
	}
	command, hasCommand, err := parseI2CData(args, "command")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if hasCommand && hasRegister {
		return ErrorResult("use either command or register, not both")
	}
	if hasCommand && len(command) == 0 {
		return ErrorResult("command must contain at least one byte")
	}
	if len(command) > 256 {
		return ErrorResult("command too long: maximum 256 bytes")
	}
	repeatedStart, err := parseI2CRepeatedStart(args)
	if err != nil {
		return ErrorResult(err.Error())
	}
	delayMS, err := parseI2CDelayMS(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	var prefix []byte
	switch {
	case hasCommand:
		prefix = command
	case hasRegister:
		prefix = []byte{byte(register)}
	}

	devPath := fmt.Sprintf("/dev/i2c-%s", bus)
	fd, err := syscall.Open(devPath, syscall.O_RDWR, 0)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to open %s: %v", devPath, err))
	}
	defer syscall.Close(fd)

	var data []byte
	switch {
	case len(prefix) == 0:
		data, err = i2cReadBytes(fd, addr, length)
	case delayMS > 0:
		// A delay cannot be expressed inside one I2C_RDWR message list, so
		// send the command, wait, then read. The STOP between phases is
		// mandatory for devices such as the AHT20.
		if err = i2cWriteBytes(fd, addr, prefix); err == nil {
			err = sleepContext(ctx, time.Duration(delayMS)*time.Millisecond)
			if err == nil {
				data, err = i2cReadBytes(fd, addr, length)
			}
		}
	case repeatedStart:
		data, err = i2cCombinedRead(fd, addr, prefix, length)
	default:
		if err = i2cWriteBytes(fd, addr, prefix); err == nil {
			data, err = i2cReadBytes(fd, addr, length)
		}
	}
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read from device 0x%02x: %v", addr, err))
	}

	hexBytes, intBytes := formatI2CBytes(data)
	fields := map[string]any{
		"bus":     devPath,
		"address": fmt.Sprintf("0x%02x", addr),
		"bytes":   intBytes,
		"hex":     hexBytes,
		"length":  len(data),
	}
	if repeatedStart {
		fields["repeated_start"] = true
	}
	if delayMS > 0 {
		fields["delay_ms"] = delayMS
	}
	result, _ := json.MarshalIndent(fields, "", "  ")
	return SilentResult(string(result))
}

// writeDevice writes bytes to an I2C device, optionally at a specific register
func (t *I2CTool) writeDevice(ctx context.Context, args map[string]any) *ToolResult {
	if err := ctx.Err(); err != nil {
		return ErrorResult(err.Error())
	}
	confirm, _ := args["confirm"].(bool)
	if !confirm {
		return ErrorResult(
			"write operations require confirm: true. Please confirm with the user before writing to I2C devices, as incorrect writes can misconfigure hardware.",
		)
	}

	bus, errResult := parseI2CBus(args)
	if errResult != nil {
		return errResult
	}
	unlock := lockI2CBus(bus)
	defer unlock()

	addr, errResult := parseI2CAddress(args)
	if errResult != nil {
		return errResult
	}

	payload, hasData, err := parseI2CData(args, "data")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if !hasData || len(payload) == 0 {
		return ErrorResult("data is required for write (array of byte values 0-255)")
	}
	if len(payload) > 256 {
		return ErrorResult("data too long: maximum 256 bytes per I2C transaction")
	}

	register, hasRegister, err := parseI2CByteArg(args, "register")
	if err != nil {
		return ErrorResult(err.Error())
	}
	delayMS, err := parseI2CDelayMS(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	data := make([]byte, 0, len(payload)+1)
	// If register is specified, prepend it to the data.
	if hasRegister {
		data = append(data, byte(register))
	}
	data = append(data, payload...)

	target := i2cTarget(bus, addr)
	policy := t.writePolicy()
	if policy.DryRun {
		if result := policy.checkTarget(target); result != nil {
			return result
		}
		hexBytes, intBytes := formatI2CBytes(data)
		result, _ := json.MarshalIndent(map[string]any{
			"dry_run":  true,
			"action":   "write",
			"bus":      fmt.Sprintf("/dev/i2c-%s", bus),
			"address":  fmt.Sprintf("0x%02x", addr),
			"bytes":    intBytes,
			"hex":      hexBytes,
			"delay_ms": delayMS,
		}, "", "  ")
		return SilentResult(string(result))
	}
	if result := policy.checkWrite(target, time.Now()); result != nil {
		return result
	}

	devPath := fmt.Sprintf("/dev/i2c-%s", bus)
	fd, err := syscall.Open(devPath, syscall.O_RDWR, 0)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to open %s: %v", devPath, err))
	}
	defer syscall.Close(fd)

	if err := i2cWriteBytes(fd, addr, data); err != nil {
		return ErrorResult(fmt.Sprintf("failed to write to device 0x%02x: %v", addr, err))
	}
	if err := sleepContext(ctx, time.Duration(delayMS)*time.Millisecond); err != nil {
		return ErrorResult(fmt.Sprintf("write completed but delay was interrupted: %v", err))
	}

	message := fmt.Sprintf("Wrote %d byte(s) to device 0x%02x on %s", len(data), addr, devPath)
	if delayMS > 0 {
		message += fmt.Sprintf("; waited %d ms", delayMS)
	}
	return SilentResult(message)
}
