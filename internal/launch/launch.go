// Package launch hands the terminal over to ssh.
//
// The important design point: sshm does not stay running underneath the
// session. Once the TUI has quit and restored the terminal, the process
// replaces itself with ssh via execve. There is no wrapper process holding the
// tty, no extra layer for signals or window-resize events to traverse, and
// nothing to leak if ssh runs for three days. When ssh exits, the user is back
// at their shell prompt exactly as if they had typed the command themselves.
package launch

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/sohail/sshm/internal/model"
)

// Binary is the ssh client to run. Overridable mainly for tests.
var Binary = "ssh"

// Plan is a resolved, ready-to-run ssh invocation.
type Plan struct {
	Path string   // absolute path to the ssh binary
	Argv []string // full argv including argv[0]
	Host model.Host
}

// Prepare resolves the ssh binary and builds argv. It is separated from Exec so
// the caller can validate everything before tearing down the TUI.
func Prepare(h model.Host, extra []string) (Plan, error) {
	if err := h.Validate(); err != nil {
		return Plan{}, err
	}
	path, err := exec.LookPath(Binary)
	if err != nil {
		return Plan{}, fmt.Errorf("cannot find %q in PATH: %w", Binary, err)
	}
	argv := append([]string{Binary}, h.SSHArgs(extra)...)
	return Plan{Path: path, Argv: argv, Host: h}, nil
}

// Exec replaces the current process with ssh. On success it does not return.
//
// Everything that must be persisted (history, config) has to be flushed before
// calling this: after execve there is no Go runtime left to run deferred code.
func (p Plan) Exec() error {
	return execve(p.Path, p.Argv, os.Environ())
}

// Run is the fallback path for platforms without execve. It runs ssh as a
// child process wired to the same stdio and waits for it.
func (p Plan) Run() error {
	cmd := exec.Command(p.Path, p.Argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
