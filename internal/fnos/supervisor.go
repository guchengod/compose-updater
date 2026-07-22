package fnos

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Supervisor struct {
	binary     string
	configPath string
	logger     *slog.Logger
	restart    chan bool
	runtime    *RuntimeStore

	mu      sync.RWMutex
	running bool
}

func NewSupervisor(binary, configPath string, logger *slog.Logger, stores ...*RuntimeStore) *Supervisor {
	var runtimeStore *RuntimeStore
	if len(stores) > 0 {
		runtimeStore = stores[0]
	}
	return &Supervisor{
		binary: binary, configPath: configPath, logger: logger,
		restart: make(chan bool, 1), runtime: runtimeStore,
	}
}

func (s *Supervisor) Run(ctx context.Context) {
	for ctx.Err() == nil {
		arguments := []string{"serve", "-config", s.configPath}
		forceRun := false
		select {
		case forceRun = <-s.restart:
		default:
		}
		if forceRun {
			arguments = append(arguments, "-run-on-start")
		}
		cmd := exec.Command(s.binary, arguments...)
		stdout := io.Writer(os.Stdout)
		if s.runtime != nil {
			stdout = io.MultiWriter(os.Stdout, s.runtime)
		}
		cmd.Stdout = stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			s.logger.Error("updater_start_failed", "error", err)
			if !waitContext(ctx, 5*time.Second) {
				return
			}
			continue
		}
		s.setRunning(true)
		s.logger.Info("updater_started", "pid", cmd.Process.Pid)
		wait := make(chan error, 1)
		go func() { wait <- cmd.Wait() }()

		select {
		case err := <-wait:
			s.setRunning(false)
			if err != nil {
				s.logger.Error("updater_exited", "error", err)
			} else {
				s.logger.Warn("updater_exited")
			}
			if !waitContext(ctx, 5*time.Second) {
				return
			}
		case forceRun = <-s.restart:
			s.logger.Info("updater_restart_requested")
			stopProcess(cmd.Process)
			<-wait
			s.setRunning(false)
			if forceRun {
				select {
				case s.restart <- true:
				default:
				}
			}
		case <-ctx.Done():
			stopProcess(cmd.Process)
			<-wait
			s.setRunning(false)
			return
		}
	}
}

func (s *Supervisor) Restart() {
	select {
	case s.restart <- false:
	default:
	}
}

func (s *Supervisor) TriggerRun() error {
	if s.runtime != nil && s.runtime.Active() {
		return fmt.Errorf("更新任务正在运行，请等待本轮完成")
	}
	select {
	case s.restart <- true:
		return nil
	default:
		return fmt.Errorf("更新服务正在处理另一个控制请求")
	}
}

func (s *Supervisor) Runtime() RuntimeSnapshot {
	if s.runtime == nil {
		return RuntimeSnapshot{}
	}
	return s.runtime.Snapshot()
}

func (s *Supervisor) Running() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

func (s *Supervisor) setRunning(running bool) {
	s.mu.Lock()
	s.running = running
	s.mu.Unlock()
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
