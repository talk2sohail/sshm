//go:build !unix

package launch

import (
	"os"
	"os/exec"
	"os/signal"
)

// execve is unavailable here -- Windows has no process-replacing exec -- so
// approximate it: run ssh as a child sharing our stdio, then exit with its
// status. From the user's point of view the difference is invisible except for
// one extra process in the tree while the session lasts.
//
// Interrupts have to be ignored in this process for the duration. A console
// Ctrl+C on Windows is delivered to every process attached to the console, not
// just the foreground one, so without this both sshm and ssh receive it: Go's
// default handler exits sshm immediately, the shell draws a fresh prompt, and
// the ssh session that is still running writes over it. Ignoring the signal
// here leaves Ctrl+C to ssh, which is the only process that should decide what
// it means -- interrupting a remote command rather than tearing down the
// session.
func execve(path string, argv, env []string) error {
	signal.Ignore(os.Interrupt)

	cmd := exec.Command(path, argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		os.Exit(ee.ExitCode())
	}
	if err != nil {
		return err
	}
	os.Exit(0)
	return nil
}
