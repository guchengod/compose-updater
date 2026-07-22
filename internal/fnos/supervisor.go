package fnos

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Supervisor struct {
	binary     string
	configPath string
	logger     *slog.Logger
	restart    chan struct{}

	mu      sync.RWMutex
	running bool
}

func NewSupervisor(binary, configPath string, logger *slog.Logger) *Supervisor {
	return &Supervisor{
		binary: binary, configPath: configPath, logger: logger,
		restart: make(chan struct{}, 1),
	}
}

func (s *Supervisor) Run(ctx context.Context) {
	for ctx.Err() == nil {
		cmd := exec.Command(s.binary, "serve", "-config", s.configPath)
		cmd.Stdout = os.Stdout
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
		case <-s.restart:
			s.logger.Info("updater_restart_requested")
			stopProcess(cmd.Process)
			<-wait
			s.setRunning(false)
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
	case s.restart <- struct{}{}:
	default:
	}
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

func stopProcess(process *os.Process) {
	if process == nil {
		return
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = process.Kill()
	}
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
