package kiro

import (
	"fmt"
	"strings"
)

type ErrorKind int

const (
	ErrUnknown        ErrorKind = iota
	ErrTokenInvalid             // force one refresh and retry, then 401
	ErrQuotaExhausted           // 402; CPA cools the credential down and fails over
	ErrForbidden                // 403 (suspended, unsubscribed, ...); CPA fails over
	ErrRateLimited              // 429; CPA applies its own backoff
	ErrBadRequest               // 400; the request itself is wrong, no failover
	ErrRetryable                // transient upstream failure, reported as 502
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
	case status == 403:
		// Any other 403 is about the account, not the request; a 400 here would
		// stop CPA from rotating to another credential.
		return ErrForbidden
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
