//go:build !linux && !darwin && !windows

package hardwaretools

import (
	"context"
	"fmt"
	"time"
)

func serialListPorts() ([]serialPortInfo, error) {
	return nil, fmt.Errorf("serial is not supported on this platform")
}

func validatePlatformSerialBaud(baud int) error {
	return fmt.Errorf("serial is not supported on this platform")
}

type serialHandle = struct{}

func serialOpenHandle(cfg serialConfig, timeout time.Duration) (serialHandle, error) {
	return serialHandle{}, fmt.Errorf("serial is not supported on this platform")
}

func serialCloseHandle(handle serialHandle) error {
	return fmt.Errorf("serial is not supported on this platform")
}

func serialSetHandleTimeout(handle serialHandle, timeout time.Duration) error {
	return fmt.Errorf("serial is not supported on this platform")
}

func serialReadHandle(ctx context.Context, handle serialHandle, length int, timeout time.Duration) ([]byte, error) {
	return nil, fmt.Errorf("serial is not supported on this platform")
}

func serialReadUntilHandle(
	ctx context.Context,
	handle serialHandle,
	delimiter []byte,
	maxLength int,
	timeout time.Duration,
) ([]byte, error) {
	return nil, fmt.Errorf("serial is not supported on this platform")
}

func serialReadAvailableHandle(
	ctx context.Context,
	handle serialHandle,
	maxLength int,
	timeout time.Duration,
) ([]byte, error) {
	return nil, fmt.Errorf("serial is not supported on this platform")
}

func serialWriteHandle(ctx context.Context, handle serialHandle, data []byte, timeout time.Duration) (int, error) {
	return 0, fmt.Errorf("serial is not supported on this platform")
}
