//go:build windows

package lock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

type platformLockState struct {
	overlapped windows.Overlapped
}

func lockPlatformFile(file *os.File, state *platformLockState) error {
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&state.overlapped,
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrAlreadyLocked
	}
	return err
}

func unlockPlatformFile(file *os.File, state *platformLockState) error {
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		1,
		0,
		&state.overlapped,
	)
}
