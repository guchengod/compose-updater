package lock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrAlreadyLocked = errors.New("已有 compose-updater 实例持有锁")

type FileLock struct {
	file  *os.File
	path  string
	state platformLockState
}

func Acquire(path string) (*FileLock, error) {
	path = filepath.Clean(path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建锁目录 %q: %w", dir, err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开锁文件 %q: %w", path, err)
	}

	lock := &FileLock{file: file, path: path}
	if err := lockPlatformFile(file, &lock.state); err != nil {
		_ = file.Close()
		if errors.Is(err, ErrAlreadyLocked) {
			return nil, ErrAlreadyLocked
		}
		return nil, fmt.Errorf("获取文件锁 %q: %w", path, err)
	}

	if err := file.Truncate(0); err == nil {
		if _, err := file.Seek(0, 0); err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Sync()
		}
	}
	return lock, nil
}

func (l *FileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockPlatformFile(l.file, &l.state)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(unlockErr, closeErr)
}
