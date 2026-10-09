//go:build darwin || linux

package mobile

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Kill the process group as well as the main decoder: codec helpers must not
// retain a private draft or outlive the request after a deadline/cancellation.
func configureMediaProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
