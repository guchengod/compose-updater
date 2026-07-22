//go:build linux || darwin

package platform

import (
	"os"

	"golang.org/x/sys/unix"
)

func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, unix.SIGTERM}
}
