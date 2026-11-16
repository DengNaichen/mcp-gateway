package fault
import (
	"fmt"
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
)

type Error struct {
	Kind          Kind
	Code, Message string
	Cause         error
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
