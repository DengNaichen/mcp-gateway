package api

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

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

func httpError(err error) *appError {
	var app *appError
	if errors.As(err, &app) {
		return app
	}
	var cross *fault.Error
	if errors.As(err, &cross) {
		return &appError{status: map[fault.Kind]int{fault.Invalid: 400, fault.NotFound: 404, fault.Forbidden: 403, fault.Conflict: 409, fault.Upstream: 502, fault.Internal: 500, fault.Unauthorized: 401, fault.RateLimited: 429, fault.TooLarge: 413}[cross.Kind], code: cross.Code, message: cross.Message, cause: cross.Cause}
	}
	var modelErr *model.Error
	if errors.As(err, &modelErr) {
		status := 400
		switch {
		case errors.Is(modelErr, model.ErrNotFound):
			status = 404
		case errors.Is(modelErr, model.ErrRateLimited):
			status = 429
		case errors.Is(modelErr, model.ErrTimeout):
			status = 504
		case errors.Is(modelErr, model.ErrUpstream):
			status = 502
		}
		return &appError{status: status, code: modelErr.Code, message: modelErr.Message, cause: modelErr.Cause, retryAfter: modelErr.RetryAfter}
	}
	var scopeErr *scope.Error
	if errors.As(err, &scopeErr) {
		status := 400
		if errors.Is(scopeErr, scope.ErrForbidden) {
			status = 403
		}
		if errors.Is(scopeErr, scope.ErrConflict) {
			status = 409
		}
		return &appError{status: status, code: scopeErr.Code, message: scopeErr.Message, cause: scopeErr.Cause}
	}
	var routingErr *routing.Error
	if errors.As(err, &routingErr) {
		status := 502
		if errors.Is(routingErr, routing.ErrInvalid) {
			status = 400
		}
		return &appError{status: status, message: routingErr.Message, cause: routingErr.Cause}
	}
	var upstreamErr *upstream.Error
	if errors.As(err, &upstreamErr) {
		status := 502
		if errors.Is(upstreamErr, upstream.ErrInvalid) {
			status = 400
		}
		return &appError{status: status, message: upstreamErr.Message, cause: upstreamErr.Cause}
	}
	var domain *maintenance.Error
	if errors.As(err, &domain) {
		status := 500
		switch domain.Kind {
		case maintenance.ErrInvalid:
			status = 400
		case maintenance.ErrNotFound:
			status = 404
		case maintenance.ErrConflict:
			status = 409
		case maintenance.ErrUnavailable:
			status = 503
		case maintenance.ErrUnauthorized:
			status = 401
		case maintenance.ErrForbidden:
			status = 403
		}
		return &appError{status: status, message: domain.Message, cause: domain.Cause}
	}
	var validation *registry.ValidationError
	if errors.As(err, &validation) {
		return &appError{status: 400, message: validation.Message, cause: validation.Cause}
	}
	var openapiValidation *openapi.ValidationError
	if errors.As(err, &openapiValidation) {
		return &appError{status: 400, message: openapiValidation.Message}
	}
	var sandboxValidation *sandbox.ValidationError
	if errors.As(err, &sandboxValidation) {
		return &appError{status: 400, message: sandboxValidation.Message}
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

// errorBody builds a hand-made error body carrying err's machine-readable code,
// so setup screens can tell an invalid key from a rate limit without parsing text.
func errorBody(message, detail string, err error) apitypes.Error {
	body := apitypes.Error{Error: message, Detail: detail}
	if app := httpError(err); app != nil {
		body.Code = app.code
	}
	return body
}

func fail(w http.ResponseWriter, err error) {
	if app := httpError(err); app != nil {
		if app.cause != nil {
			slog.Warn("gateway operation failed", "status", app.status, "error", err)
		}
		if app.retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(app.retryAfterSeconds()))
		}
		body := apitypes.Error{Error: app.message, Code: app.code}
		if app.retryAfter > 0 {
			body.RetryAfterSeconds = app.retryAfterSeconds()
		}
		respond(w, app.status, body)
		return
	}
	slog.Error("gateway operation failed", "error", err)
	respond(w, 500, apitypes.Error{Error: "操作失败，请查看后端日志"})
}
