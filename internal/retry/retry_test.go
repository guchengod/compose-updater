package retry_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guchengod/compose-updater/internal/retry"
)

var (
	errTransient = errors.New("transient network failure")
	errPermanent = errors.New("permanent authentication failure")
)

func TestDoImmediateSuccess(t *testing.T) {
	ctx := t.Context()
	var attempts int
	policy := retry.Policy{
		MaxAttempts:     3,
		InitialInterval: 10 * time.Millisecond,
		MaxInterval:     100 * time.Millisecond,
		Multiplier:      2.0,
	}

	err := retry.Do(
		ctx,
		policy,
		func(ctx context.Context) error {
			attempts++
			return nil
		},
		func(err error) bool { return true },
		func(attempt int, delay time.Duration, err error) {
			t.Fatalf("unexpected retry callback on attempt %d", attempt)
		},
	)

	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestDoEventualSuccess(t *testing.T) {
	ctx := t.Context()
	var attempts int
	var retriedAttempts []int
	policy := retry.Policy{
		MaxAttempts:     4,
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     50 * time.Millisecond,
		Multiplier:      2.0,
	}

	err := retry.Do(
		ctx,
		policy,
		func(ctx context.Context) error {
			attempts++
			if attempts < 3 {
				return errTransient
			}
			return nil
		},
		func(err error) bool { return errors.Is(err, errTransient) },
		func(attempt int, delay time.Duration, err error) {
			retriedAttempts = append(retriedAttempts, attempt)
		},
	)

	if err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if len(retriedAttempts) != 2 {
		t.Fatalf("expected 2 retries, got %d", len(retriedAttempts))
	}
	if retriedAttempts[0] != 1 || retriedAttempts[1] != 2 {
		t.Fatalf("unexpected retried attempts: %v", retriedAttempts)
	}
}

func TestDoMaxAttemptsExhausted(t *testing.T) {
	ctx := t.Context()
	var attempts int
	policy := retry.Policy{
		MaxAttempts:     3,
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     50 * time.Millisecond,
		Multiplier:      2.0,
	}

	err := retry.Do(
		ctx,
		policy,
		func(ctx context.Context) error {
			attempts++
			return errTransient
		},
		func(err error) bool { return errors.Is(err, errTransient) },
		nil,
	)

	if !errors.Is(err, errTransient) {
		t.Fatalf("expected errTransient, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts before stopping, got %d", attempts)
	}
}

func TestDoNonRetryableError(t *testing.T) {
	ctx := t.Context()
	var attempts int
	policy := retry.Policy{
		MaxAttempts:     5,
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     50 * time.Millisecond,
		Multiplier:      2.0,
	}

	err := retry.Do(
		ctx,
		policy,
		func(ctx context.Context) error {
			attempts++
			return errPermanent
		},
		func(err error) bool { return errors.Is(err, errTransient) },
		func(attempt int, delay time.Duration, err error) {
			t.Fatalf("should not retry non-retryable error")
		},
	)

	if !errors.Is(err, errPermanent) {
		t.Fatalf("expected errPermanent, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected exactly 1 attempt for non-retryable error, got %d", attempts)
	}
}

func TestDoContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var attempts atomic.Int32
	policy := retry.Policy{
		MaxAttempts:     5,
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     1 * time.Second,
		Multiplier:      2.0,
	}

	start := time.Now()
	err := retry.Do(
		ctx,
		policy,
		func(ctx context.Context) error {
			attempts.Add(1)
			cancel() // cancel during attempt 1
			return errTransient
		},
		func(err error) bool { return true },
		nil,
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if time.Since(start) > 80*time.Millisecond {
		t.Fatalf("expected immediate cancellation, took %v", time.Since(start))
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts.Load())
	}
}

func TestCalculateDelayBounds(t *testing.T) {
	policy := retry.Policy{
		MaxAttempts:     5,
		InitialInterval: 1 * time.Second,
		MaxInterval:     10 * time.Second,
		Multiplier:      2.0,
		JitterRatio:     0.1, // ±10%
	}

	// Attempt 1: base = 1s, range = [0.9s, 1.1s]
	for range 10 {
		d := retry.CalculateDelay(1, policy)
		if d < 900*time.Millisecond || d > 1100*time.Millisecond {
			t.Fatalf("attempt 1 delay out of range: %v", d)
		}
	}

	// Attempt 2: base = 2s, range = [1.8s, 2.2s]
	for range 10 {
		d := retry.CalculateDelay(2, policy)
		if d < 1800*time.Millisecond || d > 2200*time.Millisecond {
			t.Fatalf("attempt 2 delay out of range: %v", d)
		}
	}

	// Attempt 3: base = 4s, range = [3.6s, 4.4s]
	for range 10 {
		d := retry.CalculateDelay(3, policy)
		if d < 3600*time.Millisecond || d > 4400*time.Millisecond {
			t.Fatalf("attempt 3 delay out of range: %v", d)
		}
	}

	// Attempt 5: base = 16s capped at 10s, range = [9.0s, 11.0s]
	for range 10 {
		d := retry.CalculateDelay(5, policy)
		if d < 9000*time.Millisecond || d > 11000*time.Millisecond {
			t.Fatalf("attempt 5 delay capped out of range: %v", d)
		}
	}
}
