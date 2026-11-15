# Retry with Exponential Backoff Design

- **Status:** Approved
- **Date:** 2026-11-15
- **Author:** Assistant & galvin

## Overview

In `compose-updater`, automatic update workflows rely on external network requests across two primary stages:
1. **Registry metadata resolution (`internal/registry`)**: Querying tags, release dates, and authentication tokens through an optional HTTP/SOCKS5 proxy (`registry_proxy`).
2. **Container image pulling (`internal/updater`)**: Executing `docker compose pull` or `docker pull`.

Under unstable network or proxy conditions (e.g. SOCKS5/HTTP proxy drops, connection resets, gateway timeouts), transient errors currently cause the entire update cycle for a project to fail immediately without retrying.

This design introduces a unified, robust **exponential backoff retry mechanism** (`internal/retry`) with jitter, context cancellation awareness, inspectable error classification, structured logging, and configurable policy defaults.

---

## Requirements

### Functional Requirements
1. **Exponential Backoff with Jitter**:
   - Each successive retry increases delay exponentially: $T_k = \min(\text{InitialInterval} \times \text{Multiplier}^{(k-1)}, \text{MaxInterval}) \pm \text{Jitter}$.
   - Default parameters:
     - `MaxAttempts`: 4 (1 initial execution + up to 3 retries)
     - `InitialInterval`: `2s`
     - `MaxInterval`: `30s`
     - `Multiplier`: `2.0`
     - `Jitter`: `0.1` ($\pm 10\%$)
2. **Configurability**:
   - Optional `retry` block in `config.json` allowing user customization of `max_attempts`, `initial_interval`, and `max_interval`.
   - Backward-compatible: If omitted or zero-valued, sensible defaults are applied automatically.
3. **Transient vs. Terminal Error Classification**:
   - **Retryable**:
     - Network errors: connection refused, connection reset by peer, timeouts, broken pipe, EOF, DNS temporary resolution errors.
     - Proxy errors: SOCKS5 handshake failures, HTTP proxy 502 Bad Gateway / 503 Service Unavailable / 504 Gateway Timeout.
     - HTTP rate limits: 429 Too Many Requests.
     - Docker CLI transient pull failures caused by network disconnects.
   - **Non-retryable (Fast-Fail)**:
     - `context.Canceled` or `context.DeadlineExceeded` (client/system cancellation).
     - Client authentication / authorization errors: HTTP 400 Bad Request, 401 Unauthorized, 403 Forbidden.
     - Resource not found: HTTP 404 (tag or repository does not exist).
     - Configuration or syntax errors.
4. **Context Propagation & Lifecycle**:
   - Every retry step respects the parent `context.Context`. If cancelled or timed out during backoff sleep, the operation aborts immediately without hanging.
5. **Observability & Structured Logging**:
   - Log retry warnings with structured `log/slog` attributes (`attempt`, `delay`, `error`, `target`).
   - Log terminal failure once retries are exhausted.

---

## Architecture & Package Structure

```
compose-updater/
├── internal/
│   ├── retry/
│   │   ├── retry.go           # Core retry engine, Policy struct, Do function
│   │   ├── errors.go          # Error classifiers (IsRetryableNetworkError, IsRetryableHTTPStatus)
│   │   └── retry_test.go      # Comprehensive unit tests with fake clocks and mock operations
│   ├── config/
│   │   ├── config.go          # RetryConfig parsing, validation, and defaults
│   │   └── config_test.go     # Config parsing tests
│   ├── registry/
│   │   ├── resolver.go        # RemoteLister wired with retry.Do for HTTP requests
│   │   └── resolver_test.go   # Integration tests with simulated network / proxy drops
│   └── updater/
│       ├── updater.go         # ComposeClient.Pull and docker.PullImage wrapped with retry
│       └── updater_test.go    # Updater pull retry tests
```

---

## Components

### 1. `internal/retry` Package

#### Policy Specification
```go
package retry

import (
	"context"
	"time"
)

type Policy struct {
	MaxAttempts     int           // Total execution count including initial attempt (default: 4)
	InitialInterval time.Duration // Base delay before first retry (default: 2s)
	MaxInterval     time.Duration // Maximum cap for delay (default: 30s)
	Multiplier      float64       // Exponential growth factor (default: 2.0)
	JitterRatio     float64       // Random jitter ratio (default: 0.1)
}

func DefaultPolicy() Policy {
	return Policy{
		MaxAttempts:     4,
		InitialInterval: 2 * time.Second,
		MaxInterval:     30 * time.Second,
		Multiplier:      2.0,
		JitterRatio:     0.1,
	}
}
```

#### Core Execution Engine
```go
type RetryCallback func(attempt int, nextDelay time.Duration, err error)

func Do(
	ctx context.Context,
	policy Policy,
	op func(ctx context.Context) error,
	isRetryable func(error) bool,
	onRetry RetryCallback,
) error
```

- When `op(ctx)` returns `nil`, `Do` returns `nil` immediately.
- If `op(ctx)` returns an error:
  - If `attempt >= policy.MaxAttempts` or `!isRetryable(err)` or `ctx.Err() != nil`: return `err`.
  - Calculate backoff delay $D$ with jitter.
  - Call `onRetry(attempt, D, err)`.
  - Wait for $D$ or `ctx.Done()`. If `ctx.Done()` fires, return `ctx.Err()`.

### 2. Error Classifier (`internal/retry/errors.go`)

- `IsRetryableNetworkError(err error) bool`: Inspects error chain using `errors.As` and `errors.Is`:
  - `net.Error` (where `Timeout()` is true or temporary).
  - Syscall errors (`ECONNRESET`, `ECONNREFUSED`, `EPIPE`, `ETIMEDOUT`).
  - `io.EOF`, `io.ErrUnexpectedEOF`.
- `IsRetryableHTTPStatus(statusCode int) bool`:
  - `true` for 429, 500, 502, 503, 504.
  - `false` for 400, 401, 403, 404, etc.

### 3. Config Layer (`internal/config`)

In `internal/config/config.go`:
```go
type RetryConfig struct {
	MaxAttempts     int    `json:"max_attempts"`
	InitialInterval string `json:"initial_interval"`
	MaxInterval     string `json:"max_interval"`
}

func (c *Config) RetryPolicy() retry.Policy
```

Validation ensures:
- `1 <= MaxAttempts <= 10`
- `InitialInterval` and `MaxInterval` parse valid durations.
- `InitialInterval <= MaxInterval`.

### 4. Integration in `internal/registry`

In `RemoteLister`:
Wrap `l.client.Do(request)` or high-level tag query loops with `retry.Do`.
When proxy drops or returns 502/503/504/429:
- Retry with exponential backoff up to `MaxAttempts`.
- Log warning via `lister.logger` or `resolver.logger`.

### 5. Integration in `internal/updater`

In `Updater.runProject`:
Wrap `composeClient.Pull(pullCtx, activeNames)` and `u.docker.PullImage(pullCtx, image)`:
```go
err = retry.Do(pullCtx, u.cfg.RetryPolicy(), func(stepCtx context.Context) error {
    _, pullErr := composeClient.Pull(stepCtx, activeNames)
    return pullErr
}, retry.IsRetryablePullError, func(attempt int, delay time.Duration, err error) {
    u.logger.Warn("docker_pull_retrying",
        "attempt", attempt,
        "max_attempts", u.cfg.RetryPolicy().MaxAttempts,
        "backoff_duration", delay,
        "error", err,
    )
})
```

---

## Data Flow

```
[Start Update / Registry Check]
            │
            ▼
   Execute Operation (HTTP / Docker Pull)
            │
     ┌──────┴──────┐
  Success?       Failure
     │             │
     ▼             ▼
  Continue    Is Context Canceled? ──(Yes)──> Terminate (Context Error)
                   │ (No)
              Is Retryable Error? ──(No)───> Fail Immediately
                   │ (Yes)
              Attempts Remaining? ──(No)───> Return Final Error
                   │ (Yes)
              Calculate Backoff (Exponential + Jitter)
                   │
              Log Warning (slog: attempt, delay, err)
                   │
              Sleep until Delay or ctx.Done()
                   │
                   ▼
              [Retry Attempt + 1]
```

---

## Error Handling & Go Standards

Following Go engineering best practices (`cc-skills-golang` & `use-modern-go`):
- **Single Handling Rule**: Errors logged at retry points as `Warn`, never double-logged as `Error` until propagated or handled at the top level.
- **Context Awareness**: No `time.Sleep` without listening to `<-ctx.Done()`.
- **Wrapping Context**: All final errors wrapped with `fmt.Errorf("...: %w", err)`.
- **Zero-Value Usability**: `retry.Policy{}` zero-values fall back safely to defaults.

---

## Testing Strategy

1. **`internal/retry` Unit Tests**:
   - `TestRetrySuccessFirstAttempt`: Ensure 0 retries and zero delay on immediate success.
   - `TestRetryEventualSuccess`: Fails $N$ times with retryable error, succeeds on attempt $N+1$.
   - `TestRetryMaxAttemptsExhausted`: Fails continuously, confirms execution stops exactly at `MaxAttempts`.
   - `TestRetryNonRetryableError`: Fails immediately on 404 / non-retryable error without retrying.
   - `TestRetryContextCancellation`: Cancelling context during backoff wakes up immediately.
   - `TestBackoffIntervalBounds`: Mathematically verify backoff delay calculation within expected bounds.
2. **`internal/registry` Integration Tests**:
   - Mock HTTP server returns 502 Bad Gateway twice, then 200 OK with tags. Verify successful resolution.
3. **`internal/updater` Integration Tests**:
   - Fake runner fails Docker pull twice with network timeout, then succeeds. Verify update completes.
4. **Configuration Tests**:
   - Verify defaults when `retry` is omitted.
   - Verify parsing of custom `retry` block.
   - Verify validation errors for invalid numbers or intervals.

---

## Alternatives Considered

1. **HTTP-Transport Level Only**:
   - Rejected because Docker pull operations (`docker compose pull`) do not go through the Go HTTP client; they run via CLI through the Docker daemon. A transport-only solution would leave image pulls unhandled.
2. **Ad-hoc Inline Loops**:
   - Rejected because duplicating retry math and context select across `resolver.go` and `updater.go` violates DRY, creates testing gaps, and leads to inconsistent logging.
