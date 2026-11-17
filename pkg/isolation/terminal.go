package isolation

import (
	"io"
	"os"

	"golang.org/x/term"
)

// isTerminalInput reports whether stdin is a real terminal. Docker uses it for
// --tty and SSH for -tt; redirected or piped stdin must not allocate a pty.
func isTerminalInput(stdin io.Reader) bool {
	file, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}
