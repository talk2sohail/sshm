//go:build !unix

package launch

import (
	"os"
	"os/exec"
)

// execve is unavailable here, so approximate it: run ssh as a child sharing our
// stdio, then exit with its status.
func execve(path string, argv, env []string) error {
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
