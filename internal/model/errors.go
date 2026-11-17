package model

import (
	"errors"
	"fmt"
	"time"

	"gatewaylab/internal/registry"
)

var ErrInvalid = errors.New("invalid model configuration")
var ErrNotFound = errors.New("model profile not found")
var ErrUpstream = errors.New("model service failed")

// ErrRateLimited means the model provider rejected the call for rate or quota
// reasons; RetryAfter carries the provider's requested delay when known.
var ErrRateLimited = errors.New("model service rate limited")

// ErrTimeout means the model provider did not answer within the time budget.
var ErrTimeout = errors.New("model service timed out")

// Machine-readable codes for model service failures. They are stable API:
// HTTP and MCP callers branch on them to decide whether to wait, fix
// configuration, or change approach.
const (
	CodeRateLimited     = "model_rate_limited"     // short-term rate limit; retry after RetryAfter
	CodeQuotaExhausted  = "model_quota_exhausted"  // daily quota or billing exhausted; waiting minutes will not help
	CodeTimeout         = "model_timeout"          // no answer within the budget
	CodeUnavailable     = "model_unavailable"      // connection failure or provider 5xx
	CodeAuthFailed      = "model_auth_failed"      // API key invalid or lacks permission
	CodeContentBlocked  = "model_content_blocked"  // provider safety policy refused the input
	CodeRequestRejected = "model_request_rejected" // provider rejected the request as invalid
	CodeBadResponse     = "model_bad_response"     // provider answered but the answer is unusable
)

type Error struct {
	Kind    error
	Code    string
	Message string
	Cause   error
	// RetryAfter is the provider's requested delay, if it sent one.
	RetryAfter time.Duration
	// Status is the provider's HTTP status, when the failure came from a response.
	Status int
	// retryable marks transient failures that postDecisions may repeat.
	retryable bool
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}
func (e *Error) Unwrap() error        { return e.Cause }
func (e *Error) Is(target error) bool { return target == e.Kind }
func domainError(kind error, message string, cause error) error {
	return &Error{Kind: kind, Message: message, Cause: cause}
}
func invalid(message string) error { return domainError(ErrInvalid, message, nil) }

// badResponse reports a provider answer that arrived but cannot be used.
func badResponse(message string, cause error) error {
	return &Error{Kind: ErrUpstream, Code: CodeBadResponse, Message: message, Cause: cause}
}

// refused reports a provider that declined to answer (safety refusal).
func refused() error {
	return &Error{Kind: ErrUpstream, Code: CodeContentBlocked, Message: "Decisions 拒绝回答路由问题"}
}

func validateURL(value string) error {
	err := registry.ValidateURL(value)
	var e *registry.ValidationError
	if errors.As(err, &e) {
		return domainError(ErrInvalid, e.Message, e.Cause)
	}
	return err
}
