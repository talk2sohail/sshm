//go:build unix

package launch

import (
	"os"
	"syscall"
)

// execve replaces the process image. Available on Linux, macOS and the BSDs,
// which is where an ssh launcher lives.
//
// Before doing so, it reattaches stdin/stdout/stderr to the controlling
// terminal directly rather than trusting the descriptors sshm inherited.
// Something upstream in the TUI's raw-mode/input-reader teardown can leave
// fd 0 no longer recognized as a terminal by the time we get here -- that is
// exactly what produced ssh's own "stdin is not a terminal" detection, and
// forcing a pty on the ssh side alone just traded that for a connected
// session that never sees local keystrokes, because the descriptor ssh
// inherited still wasn't the real terminal. Opening /dev/tty fresh and
// dup2'ing it over 0/1/2 sidesteps whatever state the inherited fds were
// left in.
func execve(path string, argv, env []string) error {
	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		fd := int(tty.Fd())
		for _, dst := range [3]int{0, 1, 2} {
			_ = syscall.Dup2(fd, dst)
		}
		tty.Close()
	}
	return syscall.Exec(path, argv, env)
}
