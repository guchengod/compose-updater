package lock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose-updater.lock")
	first, err := Acquire(path)
	if err != nil {
		t.Fatalf("首次获取锁失败: %v", err)
	}
	defer first.Close()

	second, err := Acquire(path)
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(err, ErrAlreadyLocked) {
		t.Fatalf("第二次获取锁错误=%v，期望 ErrAlreadyLocked", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("释放锁失败: %v", err)
	}
	third, err := Acquire(path)
	if err != nil {
		t.Fatalf("释放后重新获取锁失败: %v", err)
	}
	_ = third.Close()
}
