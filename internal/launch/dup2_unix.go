//go:build unix && !linux

package launch

import "syscall"

// dup2 duplicates oldfd onto newfd. The BSDs and macOS have the dup2 syscall
// and no Dup3, which is the mirror image of newer Linux arches.
func dup2(oldfd, newfd int) error { return syscall.Dup2(oldfd, newfd) }
