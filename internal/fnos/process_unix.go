//go:build !windows

package fnos

import (
	"errors"
	"os"
	"syscall"
)

func stopProcess(process *os.Process) {
	if process == nil {
		return
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = process.Kill()
	}
}
