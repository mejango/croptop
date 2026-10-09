//go:build !darwin && !linux

package mobile

import "os/exec"

// Hosted HEIF conversion is supported on Linux and macOS only; retain portable
// compilation for the standard image formats used by other Croptop programs.
func configureMediaProcess(cmd *exec.Cmd) {}
