package maintenance

import (
	"errors"
	"fmt"

	"gatewaylab/internal/registry"
)

var (
	ErrInvalid      = errors.New("invalid maintenance input")
	ErrNotFound     = errors.New("maintenance item not found")
	ErrConflict     = errors.New("maintenance conflict")
	ErrUnavailable  = errors.New("maintenance unavailable")
	ErrUnauthorized = errors.New("maintenance unauthorized")
	ErrForbidden    = errors.New("maintenance forbidden")
)

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

var validID = registry.ValidID
var compileSchema = registry.CompileSchema
var validateMapping = registry.ValidateMapping
var validateURL = registry.ValidateURL
