package model

import (
	"errors"
	"fmt"

	"gatewaylab/internal/registry"
)

var ErrInvalid = errors.New("invalid model configuration")
var ErrNotFound = errors.New("model profile not found")
var ErrUpstream = errors.New("model service failed")

type Error struct {
	Kind    error
	Message string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}
func (e *Error) Unwrap() error                                  { return e.Cause }
func (e *Error) Is(target error) bool                           { return target == e.Kind }
func domainError(kind error, message string, cause error) error { return &Error{kind, message, cause} }
func invalid(message string) error                              { return domainError(ErrInvalid, message, nil) }
func validateURL(value string) error {
	err := registry.ValidateURL(value)
	var e *registry.ValidationError
	if errors.As(err, &e) {
		return domainError(ErrInvalid, e.Message, e.Cause)
	}
	return err
}
