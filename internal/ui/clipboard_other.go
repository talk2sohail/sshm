//go:build !windows

package ui

// nativeCopy is a no-op away from Windows: OSC 52 is the portable path and the
// terminals people use on Linux and macOS honour it, so there is nothing to
// shell out to.
func nativeCopy(string) error { return nil }
