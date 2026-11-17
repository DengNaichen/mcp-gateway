package fault

import (
	"fmt"
	"time"
)

type Kind uint8

const (
	Invalid Kind = iota
	NotFound
	Forbidden
	Conflict
	Upstream
	Internal
	Unauthorized
	RateLimited
	TooLarge
	Timeout
	Unavailable
)

type Error struct {
	Kind          Kind
	Code, Message string
	Cause         error
	RetryAfter    time.Duration
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}
func (e *Error) Unwrap() error { return e.Cause }
func New(kind Kind, message string, cause error) error {
	return &Error{Kind: kind, Message: message, Cause: cause}
}

// NewDetailed constructs an Error with an optional machine-readable code and retry delay.
func NewDetailed(kind Kind, code, message string, retryAfter time.Duration, cause error) error {
	return &Error{Kind: kind, Code: code, Message: message, RetryAfter: retryAfter, Cause: cause}
}

// WithCode attaches a machine-readable code to err, cloning *Error values instead of mutating in place.
func WithCode(err error, code string) error {
	if err == nil {
		return nil
	}
	if fe, ok := err.(*Error); ok {
		copyErr := *fe
		copyErr.Code = code
		return &copyErr
	}
	return &Error{Kind: Internal, Code: code, Message: err.Error(), Cause: err}
}

// WithRetryAfter attaches a retry delay to err, cloning *Error values instead of mutating in place.
func WithRetryAfter(err error, retryAfter time.Duration) error {
	if err == nil {
		return nil
	}
	if fe, ok := err.(*Error); ok {
		copyErr := *fe
		copyErr.RetryAfter = retryAfter
		return &copyErr
	}
	return &Error{Kind: RateLimited, Message: err.Error(), RetryAfter: retryAfter, Cause: err}
}
