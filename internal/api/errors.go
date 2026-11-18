package api

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"gatewaylab/internal/secrets"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/model"
	"gatewaylab/internal/openapi"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/upstream"
)

type appError struct {
	status  int
	code    string
	message string
	cause   error
	// retryAfter is surfaced as the Retry-After header and retry_after_seconds.
	retryAfter time.Duration
}

func (e *appError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.message, e.cause)
	}
	return e.message
}
func (e *appError) Unwrap() error { return e.cause }
func problem(status int, message string, cause error) error {
	return &appError{status: status, message: message, cause: cause}
}
func invalid(message string) error { return problem(http.StatusBadRequest, message, nil) }

var faultHTTPStatus = map[fault.Kind]int{
	fault.Invalid:      http.StatusBadRequest,
	fault.NotFound:     http.StatusNotFound,
	fault.Forbidden:    http.StatusForbidden,
	fault.Conflict:     http.StatusConflict,
	fault.Upstream:     http.StatusBadGateway,
	fault.Internal:     http.StatusInternalServerError,
	fault.Unauthorized: http.StatusUnauthorized,
	fault.RateLimited:  http.StatusTooManyRequests,
	fault.TooLarge:     http.StatusRequestEntityTooLarge,
	fault.Timeout:      http.StatusGatewayTimeout,
	fault.Unavailable:  http.StatusServiceUnavailable,
}

var maintenanceHTTPStatus = map[error]int{
	maintenance.ErrInvalid:      http.StatusBadRequest,
	maintenance.ErrNotFound:     http.StatusNotFound,
	maintenance.ErrConflict:     http.StatusConflict,
	maintenance.ErrUnavailable:  http.StatusServiceUnavailable,
	maintenance.ErrUnauthorized: http.StatusUnauthorized,
	maintenance.ErrForbidden:    http.StatusForbidden,
}

func modelHTTPStatus(err error) int {
	for _, entry := range []struct {
		target error
		status int
	}{
		{model.ErrNotFound, http.StatusNotFound},
		{model.ErrRateLimited, http.StatusTooManyRequests},
		{model.ErrTimeout, http.StatusGatewayTimeout},
		{model.ErrUpstream, http.StatusBadGateway},
	} {
		if errors.Is(err, entry.target) {
			return entry.status
		}
	}
	return http.StatusBadRequest
}

func httpError(err error) *appError {
	var app *appError
	if errors.As(err, &app) {
		return app
	}
	var cross *fault.Error
	if errors.As(err, &cross) {
		status := faultHTTPStatus[cross.Kind]
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return &appError{status: status, code: cross.Code, message: cross.Message, cause: cross.Cause, retryAfter: cross.RetryAfter}
	}
	var modelErr *model.Error
	if errors.As(err, &modelErr) {
		return &appError{status: modelHTTPStatus(modelErr), code: modelErr.Code, message: modelErr.Message, cause: modelErr.Cause, retryAfter: modelErr.RetryAfter}
	}
	var scopeErr *scope.Error
	if errors.As(err, &scopeErr) {
		status := http.StatusBadRequest
		if errors.Is(scopeErr, scope.ErrForbidden) {
			status = http.StatusForbidden
		} else if errors.Is(scopeErr, scope.ErrConflict) {
			status = http.StatusConflict
		}
		return &appError{status: status, code: scopeErr.Code, message: scopeErr.Message, cause: scopeErr.Cause}
	}
	var routingErr *routing.Error
	if errors.As(err, &routingErr) {
		status := http.StatusBadGateway
		if errors.Is(routingErr, routing.ErrInvalid) {
			status = http.StatusBadRequest
		}
		return &appError{status: status, message: routingErr.Message, cause: routingErr.Cause}
	}
	var upstreamErr *upstream.Error
	if errors.As(err, &upstreamErr) {
		status := http.StatusBadGateway
		if errors.Is(upstreamErr, upstream.ErrInvalid) {
			status = http.StatusBadRequest
		}
		return &appError{status: status, message: upstreamErr.Message, cause: upstreamErr.Cause}
	}
	var domain *maintenance.Error
	if errors.As(err, &domain) {
		status := maintenanceHTTPStatus[domain.Kind]
		if status == 0 {
			status = http.StatusInternalServerError
		}
		return &appError{status: status, message: domain.Message, cause: domain.Cause}
	}
	var credentialErr *secrets.Error
	if errors.As(err, &credentialErr) {
		return &appError{status: http.StatusServiceUnavailable, message: credentialErr.Message}
	}
	var validation *registry.ValidationError
	if errors.As(err, &validation) {
		return &appError{status: http.StatusBadRequest, message: validation.Message, cause: validation.Cause}
	}
	var openapiValidation *openapi.ValidationError
	if errors.As(err, &openapiValidation) {
		return &appError{status: http.StatusBadRequest, message: openapiValidation.Message}
	}
	var sandboxValidation *sandbox.ValidationError
	if errors.As(err, &sandboxValidation) {
		return &appError{status: http.StatusBadRequest, message: sandboxValidation.Message}
	}
	return nil
}

// retryAfterSeconds rounds up: waiting less than the provider asked is useless.
func (e *appError) retryAfterSeconds() int {
	return int(math.Ceil(e.retryAfter.Seconds()))
}

// payload is the machine-readable error body shared by HTTP responses and MCP
// tool results: callers branch on code and retry_after_seconds, not on text.
func (e *appError) payload(message string) map[string]any {
	out := map[string]any{"error": message}
	if e.code != "" {
		out["code"] = e.code
	}
	if e.retryAfter > 0 {
		out["retry_after_seconds"] = e.retryAfterSeconds()
	}
	return out
}

// failWithDetail writes an error response preserving err's HTTP status,
// machine-readable code, and Retry-After header while overriding the message
// and attaching redacted diagnostic details.
func failWithDetail(w http.ResponseWriter, message, detail string, err error) {
	failWithMessage(w, message, detail, err)
}

func fail(w http.ResponseWriter, err error) {
	failWithMessage(w, "", "", err)
}

func failWithMessage(w http.ResponseWriter, message, detail string, err error) {
	if app := httpError(err); app != nil {
		if app.cause != nil {
			slog.Warn("gateway operation failed", "status", app.status, "error", err)
		}
		msg := app.message
		if message != "" {
			msg = message
		}
		if app.retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(app.retryAfterSeconds()))
		}
		body := apitypes.Error{Error: msg, Detail: detail, Code: app.code}
		if app.retryAfter > 0 {
			body.RetryAfterSeconds = app.retryAfterSeconds()
		}
		respond(w, app.status, body)
		return
	}
	slog.Error("gateway operation failed", "error", err)
	msg := "操作失败，请查看后端日志"
	if message != "" {
		msg = message
	}
	respond(w, 500, apitypes.Error{Error: msg, Detail: detail})
}
