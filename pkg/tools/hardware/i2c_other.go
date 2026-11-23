//go:build !linux

package hardwaretools

import "context"

// scan is a stub for non-Linux platforms.
func (t *I2CTool) scan(args map[string]any) *ToolResult {
	return ErrorResult("I2C is only supported on Linux")
}

// readDevice is a stub for non-Linux platforms.
func (t *I2CTool) readDevice(ctx context.Context, args map[string]any) *ToolResult {
	return ErrorResult("I2C is only supported on Linux")
}

// writeDevice is a stub for non-Linux platforms.
func (t *I2CTool) writeDevice(ctx context.Context, args map[string]any) *ToolResult {
	return ErrorResult("I2C is only supported on Linux")
}
