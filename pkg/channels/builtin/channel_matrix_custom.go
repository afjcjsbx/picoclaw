//go:build custom_channels && channel_matrix && !mipsle && !netbsd && !(freebsd && arm) && !android

package builtin

import (
	_ "github.com/sipeed/picoclaw/pkg/channels/matrix"
)
