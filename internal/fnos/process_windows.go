//go:build windows

package fnos

import "os"

func stopProcess(process *os.Process) {
	if process != nil {
		_ = process.Kill()
	}
}
