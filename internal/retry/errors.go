package retry

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
)

// IsRetryableHTTPStatus returns true for HTTP status codes that represent
// transient failures or rate limiting.
func IsRetryableHTTPStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// IsRetryableNetworkError determines whether an error from an HTTP client, proxy,
// or low-level network connection is transient and safe to retry.
func IsRetryableNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// Check net.Error
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
	}

	// Check syscall.Errno
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.ECONNREFUSED, syscall.ECONNRESET, syscall.ETIMEDOUT, syscall.EPIPE:
			return true
		}
	}

	// Fallback to text inspections for proxy/driver/tls errors that wrap unexported types
	msg := strings.ToLower(err.Error())
	retryableKeywords := []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"i/o timeout",
		"tls handshake timeout",
		"handshake timeout",
		"unexpected eof",
		"socks connect",
		"socks server failure",
		"proxyconnect",
		"proxy",
		"server misbehaving",
		"temporary failure in name resolution",
		"no such host",
		"read: connection reset by peer",
		"client.timeout exceeded",
	}

	for _, kw := range retryableKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}

	return false
}

// IsRetryablePullError determines whether an error resulting from docker pull
// or docker compose pull is caused by transient network or proxy instability.
func IsRetryablePullError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	// Check if the underlying cause is a standard retryable network error
	if IsRetryableNetworkError(err) {
		return true
	}

	msg := strings.ToLower(err.Error())

	// Non-retryable permanent docker errors: fast fail
	nonRetryableKeywords := []string{
		"manifest unknown",
		"not found",
		"denied",
		"unauthorized",
		"invalid reference format",
		"unknown flag",
	}
	for _, kw := range nonRetryableKeywords {
		if strings.Contains(msg, kw) {
			return false
		}
	}

	// Retryable pull symptoms (Docker daemon output)
	retryablePullKeywords := []string{
		"failed to fetch oauth token",
		"error pulling image configuration",
		"download failed after",
		"received unexpected http status: 5",
		"502 bad gateway",
		"503 service unavailable",
		"504 gateway timeout",
		"request canceled while waiting for connection",
		"error dialing",
		"dial tcp",
	}

	for _, kw := range retryablePullKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}

	return false
}
