# Retry with Exponential Backoff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement exponential backoff retry with jitter for Registry API queries and Docker image pulls to handle unstable proxy and network conditions.

**Architecture:** A standalone, context-aware `internal/retry` engine coordinates exponential delay calculation with jitter and error classification. `internal/config` provides backward-compatible optional configuration, and `internal/registry` and `internal/updater` wire into the retry engine with structured `slog` observability.

**Tech Stack:** Go 1.22+, `net/http`, `log/slog`, `context`, `time`, `testing`

---

## File Structure

- Create: `internal/retry/retry.go` (Policy definition, delay calculation with jitter, `Do` execution loop)
- Create: `internal/retry/errors.go` (Classification of transient network, proxy, and HTTP status errors)
- Create: `internal/retry/retry_test.go` (Unit tests covering retries, cancellations, bounds, fast-fail)
- Modify: `internal/config/config.go` (Add `RetryConfig` to `Config`, validation, and `RetryPolicy()` accessor)
- Modify: `internal/config/config_test.go` (Tests for default and custom retry configuration)
- Modify: `internal/registry/resolver.go` (Integrate retry into `RemoteLister` HTTP calls)
- Modify: `internal/registry/resolver_test.go` (Add mock server tests for transient 502/network failure retry recovery)
- Modify: `internal/updater/updater.go` (Wrap `composeClient.Pull` and `u.docker.PullImage` with retry)
- Modify: `internal/updater/updater_test.go` (Add tests for pull retry behavior)

---

### Task 1: Create `internal/retry` Core Engine

**Files:**
- Create: `internal/retry/retry.go`
- Create: `internal/retry/retry_test.go`

- [ ] **Step 1: Write failing tests for retry engine**
  Create `internal/retry/retry_test.go` with tests:
  - Immediate success without retry.
  - Eventual success after transient errors.
  - Maximum attempts exhaustion.
  - Immediate abort when context is canceled.
  - Non-retryable error aborts immediately.

- [ ] **Step 2: Run test to verify it fails**
  Run: `go test ./internal/retry`
  Expected: FAIL (package or functions not implemented).

- [ ] **Step 3: Implement `internal/retry/retry.go`**
  Implement `Policy`, `DefaultPolicy()`, `CalculateDelay(attempt int, policy Policy) time.Duration`, and `Do(ctx, policy, op, isRetryable, onRetry) error`. Ensure context cancellation is checked before and during sleep.

- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v -race ./internal/retry`
  Expected: PASS.

- [ ] **Step 5: Commit**
  `git add internal/retry && git commit -m "feat(retry): implement core exponential backoff retry engine"`

---

### Task 2: Implement Error Classification in `internal/retry/errors.go`

**Files:**
- Create: `internal/retry/errors.go`
- Modify: `internal/retry/retry_test.go`

- [ ] **Step 1: Write failing tests for error classifier**
  Add unit tests in `internal/retry/retry_test.go`:
  - Network errors (timeout, connection reset, ECONNREFUSED) recognized as retryable.
  - HTTP 429, 500, 502, 503, 504 recognized as retryable.
  - HTTP 400, 401, 403, 404 recognized as non-retryable.
  - Context canceled / deadline exceeded recognized as non-retryable.

- [ ] **Step 2: Run test to verify it fails**
  Run: `go test ./internal/retry`
  Expected: FAIL.

- [ ] **Step 3: Implement `internal/retry/errors.go`**
  Implement `IsRetryableNetworkError(err error) bool` and `IsRetryableHTTPStatus(statusCode int) bool`.

- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/retry`
  Expected: PASS.

- [ ] **Step 5: Commit**
  `git add internal/retry/errors.go internal/retry/retry_test.go && git commit -m "feat(retry): add network and http error classification"`

---

### Task 3: Add `RetryConfig` to `internal/config`

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing tests for RetryConfig**
  In `internal/config/config_test.go`, add test cases:
  - Default retry policy when `retry` is omitted.
  - Custom retry configuration parsing (`max_attempts`, `initial_interval`, `max_interval`).
  - Validation error on invalid interval format or attempts > 10.

- [ ] **Step 2: Run test to verify it fails**
  Run: `go test ./internal/config`
  Expected: FAIL.

- [ ] **Step 3: Implement RetryConfig in `internal/config/config.go`**
  Add `RetryConfig` struct, validation in `validate()`, and `RetryPolicy()` method returning `retry.Policy`.

- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/config`
  Expected: PASS.

- [ ] **Step 5: Commit**
  `git add internal/config && git commit -m "feat(config): add retry configuration and policy accessor"`

---

### Task 4: Integrate Retry into Registry Resolver

**Files:**
- Modify: `internal/registry/resolver.go`
- Modify: `internal/registry/resolver_test.go`

- [ ] **Step 1: Write failing test in `internal/registry/resolver_test.go`**
  Add test simulating proxy / registry 502 error followed by recovery on 2nd attempt using `httptest.Server`.

- [ ] **Step 2: Run test to verify it fails**
  Run: `go test ./internal/registry -run TestResolverRetryOnTransientFailure`
  Expected: FAIL.

- [ ] **Step 3: Implement retry integration in `internal/registry/resolver.go`**
  Pass retry policy into `RemoteLister` or `Resolver`, wrap HTTP request execution in `listDockerHubTags` and `authorizedGet` with `retry.Do`, logging warnings on retries.

- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/registry`
  Expected: PASS.

- [ ] **Step 5: Commit**
  `git add internal/registry && git commit -m "feat(registry): integrate exponential backoff retry for registry queries"`

---

### Task 5: Integrate Retry into Updater Pull Operations

**Files:**
- Modify: `internal/updater/updater.go`
- Modify: `internal/updater/updater_test.go`

- [ ] **Step 1: Write failing test in `internal/updater/updater_test.go`**
  Add test verifying updater retries pull on transient runner failure.

- [ ] **Step 2: Run test to verify it fails**
  Run: `go test ./internal/updater -run TestUpdaterPullRetries`
  Expected: FAIL.

- [ ] **Step 3: Implement pull retry in `internal/updater/updater.go`**
  Wrap `composeClient.Pull` and `u.docker.PullImage` with `retry.Do`, logging `docker_pull_retrying` on retry attempts.

- [ ] **Step 4: Run test to verify it passes**
  Run: `go test -v ./internal/updater`
  Expected: PASS.

- [ ] **Step 5: Commit**
  `git add internal/updater && git commit -m "feat(updater): integrate exponential backoff retry for docker pull"`

---

### Task 6: Full Regression Verification & Documentation Update

**Files:**
- Modify: `README.md`
- Modify: `config.example.json`

- [ ] **Step 1: Run complete test suite**
  Run: `go test -race ./...`
  Expected: All packages PASS.

- [ ] **Step 2: Update documentation and example config**
  Document the optional `retry` section in `README.md` and `config.example.json`.

- [ ] **Step 3: Commit**
  `git add README.md config.example.json && git commit -m "docs: document retry configuration options"`
