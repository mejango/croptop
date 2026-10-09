//go:build windows

package update

import "errors"

func execSelf(exe string, args, env []string) error {
	return errors.New("exec not available on windows")
}
