package hardwaretools

import (
	"context"
	"time"
)

func serialRead(ctx context.Context, cfg serialConfig, length int, timeout time.Duration) ([]byte, error) {
	if err := serialContextErr(ctx); err != nil {
		return nil, err
	}
	handle, err := serialOpenHandle(cfg, timeout)
	if err != nil {
		return nil, err
	}
	defer serialCloseHandle(handle)
	return serialReadHandle(ctx, handle, length, timeout)
}

func serialReadUntil(
	ctx context.Context,
	cfg serialConfig,
	delimiter []byte,
	maxLength int,
	timeout time.Duration,
) ([]byte, error) {
	if err := serialContextErr(ctx); err != nil {
		return nil, err
	}
	handle, err := serialOpenHandle(cfg, min(timeout, 100*time.Millisecond))
	if err != nil {
		return nil, err
	}
	defer serialCloseHandle(handle)
	return serialReadUntilHandle(ctx, handle, delimiter, maxLength, timeout)
}

func serialReadAvailable(
	ctx context.Context,
	cfg serialConfig,
	maxLength int,
	timeout time.Duration,
) ([]byte, error) {
	if err := serialContextErr(ctx); err != nil {
		return nil, err
	}
	handle, err := serialOpenHandle(cfg, min(timeout, 100*time.Millisecond))
	if err != nil {
		return nil, err
	}
	defer serialCloseHandle(handle)
	return serialReadAvailableHandle(ctx, handle, maxLength, timeout)
}

func serialWrite(ctx context.Context, cfg serialConfig, data []byte, timeout time.Duration) (int, error) {
	if err := serialContextErr(ctx); err != nil {
		return 0, err
	}
	handle, err := serialOpenHandle(cfg, timeout)
	if err != nil {
		return 0, err
	}
	defer serialCloseHandle(handle)
	return serialWriteHandle(ctx, handle, data, timeout)
}
