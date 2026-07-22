package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultOutputLimit = 2 << 20

type Runner struct {
	OutputLimit int
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

type CommandError struct {
	Command  string
	Args     []string
	ExitCode int
	Stderr   string
	Cause    error
}

func (e *CommandError) Error() string {
	message := strings.TrimSpace(e.Stderr)
	if message == "" {
		message = e.Cause.Error()
	}
	return fmt.Sprintf("命令失败(exit=%d): %s %s: %s", e.ExitCode, e.Command, strings.Join(e.Args, " "), message)
}

func (e *CommandError) Unwrap() error { return e.Cause }

func (r Runner) Run(ctx context.Context, dir string, env map[string]string, name string, args ...string) (Result, error) {
	limit := r.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	stdout := newLimitedBuffer(limit)
	stderr := newLimitedBuffer(limit)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = mergeEnv(os.Environ(), env)

	started := time.Now()
	err := cmd.Run()
	result := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: time.Since(started),
	}
	if err == nil {
		return result, nil
	}

	result.ExitCode = -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("命令执行超时: %w", ctx.Err())
	} else if errors.Is(ctx.Err(), context.Canceled) {
		err = fmt.Errorf("命令已取消: %w", ctx.Err())
	}
	return result, &CommandError{
		Command:  name,
		Args:     append([]string(nil), args...),
		ExitCode: result.ExitCode,
		Stderr:   result.Stderr,
		Cause:    err,
	}
}

func mergeEnv(base []string, extra map[string]string) []string {
	type envValue struct {
		key   string
		value string
	}
	values := make(map[string]envValue, len(base)+len(extra))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		values[normalizeEnvKey(key)] = envValue{key: key, value: value}
	}
	for key, value := range extra {
		if key == "" {
			continue
		}
		values[normalizeEnvKey(key)] = envValue{key: key, value: value}
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		result = append(result, item.key+"="+item.value)
	}
	return result
}

type limitedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func newLimitedBuffer(max int) *limitedBuffer {
	return &limitedBuffer{max: max}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.max - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(p)
	return original, nil
}

func (b *limitedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + "\n...[输出已截断]"
	}
	return b.buf.String()
}
