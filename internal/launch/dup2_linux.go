//go:build linux

package launch

import "syscall"

// dup2 duplicates oldfd onto newfd.
//
// Linux is split here: syscall.Dup2 is absent on the arches whose kernels never
// had the dup2 syscall -- arm64, riscv64, loong64 -- which is why building for a
// Raspberry Pi or a Graviton instance failed while amd64 was fine. Dup3 is
// present on every Linux arch, and with no flags it is exactly Dup2. It does
// reject oldfd == newfd, unlike Dup2; the caller only ever dups a freshly opened
// /dev/tty (so fd 3 or higher) onto 0, 1 and 2, so that cannot arise.
func dup2(oldfd, newfd int) error { return syscall.Dup3(oldfd, newfd, 0) }
