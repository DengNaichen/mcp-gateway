package scope

import (
	"errors"
	"fmt"
)

var ErrInvalid = errors.New("invalid scope")
var ErrForbidden = errors.New("scope denied")
var ErrConflict = errors.New("scope conflict")

type Error struct {
	Kind          error
	Code, Message string
	Cause         error
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
