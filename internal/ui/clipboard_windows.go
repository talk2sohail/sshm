//go:build windows

package ui

import (
	"os/exec"
	"strings"
)

// nativeCopy puts s on the Windows clipboard using clip.exe.
//
// OSC 52 alone is not enough here. Windows Terminal understands it, but
// conhost -- the classic console host that still backs plenty of PowerShell
// windows, and everything launched from the Start menu on older builds --
// ignores the sequence silently, so the yank key appeared to work and nothing
// was ever copied. clip.exe ships with Windows, needs no cooperation from the
// terminal, and is fed through a pipe so it never touches our own stdio.
//
// Text is written as UTF-8. clip.exe interprets its input in the console code
// page, so a non-ASCII character in a path can come out garbled; an ssh command
// line is ASCII in all but the rarest cases, and that beats copying nothing.
func nativeCopy(s string) error {
	cmd := exec.Command("clip.exe")
	cmd.Stdin = strings.NewReader(s)
	return cmd.Run()
}
