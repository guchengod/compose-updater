//go:build linux || darwin

package lock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type platformLockState struct{}

func lockPlatformFile(file *os.File, _ *platformLockState) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrAlreadyLocked
	}
	return err
}

func unlockPlatformFile(file *os.File, _ *platformLockState) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
