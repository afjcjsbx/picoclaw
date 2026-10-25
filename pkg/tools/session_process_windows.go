//go:build windows

package tools

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func killProcessGroup(pid int) error {
	out, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err != nil {
		// taskkill exits with 128 when the process (or tree) no longer
		// exists, which is the same end state as a successful kill.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 {
			return nil
		}
		return fmt.Errorf("failed to kill process %d: %w (%s)", pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}
