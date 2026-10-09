//go:build !windows

package mobile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Kernel locks are released on process death, so a crash never strands a
// sentinel file that prevents the journal recovery path from starting.
func lockDirectory(dir string) (io.Closer, error) {
	f, err := os.OpenFile(filepath.Join(dir, "service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another phone publisher already owns this private data directory: %w", err)
	}
	return f, nil
}
