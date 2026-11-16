package upstream

import (
	"errors"
	"fmt"
)

var ErrInvalid = errors.New("invalid upstream definition")
var ErrUpstream = errors.New("upstream failure")

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
func (e *Error) Unwrap() error        { return e.Cause }
func (e *Error) Is(target error) bool { return target == e.Kind }
func invalid(message string) error    { return &Error{Kind: ErrInvalid, Message: message} }
func failure(message string, cause error) error {
	return &Error{Kind: ErrUpstream, Message: message, Cause: cause}
}
