//go:build !windows

package update

import "syscall"

func execSelf(exe string, args, env []string) error { return syscall.Exec(exe, args, env) }
