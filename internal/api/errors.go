package api

import (
	"errors"
	"fmt"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/model"
	"gatewaylab/internal/openapi"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/upstream"
	"log/slog"
	"net/http"
)

type appError struct {
	status  int
	code    string
	message string
	cause   error
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
		if errors.Is(modelErr, model.ErrNotFound) {
			status = 404
		}
		if errors.Is(modelErr, model.ErrUpstream) {
			status = 502
		}
		return &appError{status: status, message: modelErr.Message, cause: modelErr.Cause}
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
func fail(w http.ResponseWriter, err error) {
	if app := httpError(err); app != nil {
		if app.cause != nil {
			slog.Warn("gateway operation failed", "status", app.status, "error", err)
		}
		payload := map[string]any{"error": app.message}
		if app.code != "" {
			payload["code"] = app.code
		}
		respond(w, app.status, payload)
		return
	}
	slog.Error("gateway operation failed", "error", err)
	respond(w, 500, map[string]string{"error": "操作失败，请查看后端日志"})
}
