package retry

import (
	"context"
	"math"
	"math/rand/v2"
	"time"
)

// Policy defines parameters for exponential backoff retry.
type Policy struct {
	MaxAttempts     int           // Maximum total attempts (initial + retries). Default: 4.
	InitialInterval time.Duration // Initial delay duration before the first retry. Default: 2s.
	MaxInterval     time.Duration // Maximum delay duration cap. Default: 30s.
	Multiplier      float64       // Factor by which delay increases each retry. Default: 2.0.
	JitterRatio     float64       // Random jitter ratio (e.g. 0.1 for ±10%). Default: 0.1.
}

// DefaultPolicy returns a production-ready retry policy.
func DefaultPolicy() Policy {
	return Policy{
		MaxAttempts:     4,
		InitialInterval: 2 * time.Second,
		MaxInterval:     30 * time.Second,
		Multiplier:      2.0,
		JitterRatio:     0.1,
	}
}

// Normalize ensures that zero or invalid values in Policy are replaced with safe defaults.
func (p Policy) Normalize() Policy {
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = 4
	}
	if p.InitialInterval <= 0 {
		p.InitialInterval = 2 * time.Second
	}
	if p.MaxInterval <= 0 {
		p.MaxInterval = 30 * time.Second
	}
	if p.MaxInterval < p.InitialInterval {
		p.MaxInterval = p.InitialInterval
	}
	if p.Multiplier <= 1.0 {
		p.Multiplier = 2.0
	}
	if p.JitterRatio < 0 || p.JitterRatio >= 1.0 {
		p.JitterRatio = 0.1
	}
	return p
}

// CalculateDelay computes the sleep duration for attempt (1-based, where attempt 1 is the 1st retry delay).
func CalculateDelay(attempt int, policy Policy) time.Duration {
	policy = policy.Normalize()
	if attempt < 1 {
		attempt = 1
	}

	// Exponential backoff: InitialInterval * Multiplier^(attempt - 1)
	factor := math.Pow(policy.Multiplier, float64(attempt-1))
	baseDelaySec := policy.InitialInterval.Seconds() * factor
	maxDelaySec := policy.MaxInterval.Seconds()
	if baseDelaySec > maxDelaySec || math.IsInf(baseDelaySec, 0) {
		baseDelaySec = maxDelaySec
	}

	// Apply jitter: ±(baseDelaySec * JitterRatio)
	if policy.JitterRatio > 0 {
		// rand.Float64() returns [0.0, 1.0), so (2 * rand - 1) is in [-1.0, 1.0)
		delta := (rand.Float64()*2 - 1) * (baseDelaySec * policy.JitterRatio)
		baseDelaySec = max(0, baseDelaySec+delta)
	}

	return time.Duration(baseDelaySec * float64(time.Second))
}

// RetryCallback is invoked when an attempt fails and will be retried.
type RetryCallback func(attempt int, nextDelay time.Duration, err error)

// Do executes an operation with exponential backoff retries.
// It stops when:
// - op succeeds (returns nil)
// - ctx is canceled or times out
// - err is deemed non-retryable by isRetryable
// - attempt count reaches policy.MaxAttempts
func Do(
	ctx context.Context,
	policy Policy,
	op func(ctx context.Context) error,
	isRetryable func(error) bool,
	onRetry RetryCallback,
) error {
	policy = policy.Normalize()

	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := op(ctx)
		if err == nil {
			return nil
		}

		// If context was canceled or deadline exceeded, prioritize context error
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// If this was the last attempt, or error is not retryable: return
		if attempt >= policy.MaxAttempts {
			return err
		}
		if isRetryable != nil && !isRetryable(err) {
			return err
		}

		delay := CalculateDelay(attempt, policy)
		if onRetry != nil {
			onRetry(attempt, delay, err)
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	return nil
}
