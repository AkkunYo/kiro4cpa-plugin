package kiro

import (
	"fmt"
	"strings"
)

type ErrorKind int

const (
	ErrUnknown        ErrorKind = iota
	ErrTokenInvalid             // force one refresh, then retry
	ErrQuotaExhausted           // disable the credential and fail over
	ErrSuspended                // account locked by upstream
	ErrRateLimited              // cool down using response headers
	ErrBadRequest               // the request itself is wrong; do not retry
	ErrRetryable                // transient upstream failure
)

type UpstreamError struct {
	Status int
	Kind   ErrorKind
	Body   string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("kiro upstream HTTP %d: %s", e.Status, e.Body)
}

const maxErrorBody = 512

// classifyUpstream follows kiro.rs provider.rs error handling.
func classifyUpstream(status int, body []byte) *UpstreamError {
	text := string(body)
	if len(text) > maxErrorBody {
		text = text[:maxErrorBody]
	}
	return &UpstreamError{Status: status, Kind: kindOf(status, text), Body: text}
}

func kindOf(status int, body string) ErrorKind {
	switch {
	case status == 401:
		return ErrTokenInvalid
	case status == 403 && strings.Contains(body, "The bearer token included in the request is invalid"):
		return ErrTokenInvalid
	case status == 403 && strings.Contains(body, "suspended") && strings.Contains(body, "locked your account"):
		return ErrSuspended
	case status == 402:
		return ErrQuotaExhausted
	case status == 429:
		return ErrRateLimited
	case status == 408 || status == 524 || status >= 500:
		return ErrRetryable
	case status >= 400:
		return ErrBadRequest
	default:
		return ErrUnknown
	}
}
