package routing

import (
	"context"
	"errors"
	"fmt"

	"gatewaylab/internal/registry"
)

// Client supplies model answers; routing owns the selection policy and prompts.
// Implementations must return one relevance per instruction, in the same order.
type Client interface {
	Decisions(context.Context, string, []string) (Answers, error)
	Choose(context.Context, string, string, []Choice) (ChoiceAnswer, Usage, error)
}
type Server = registry.Server
type Request struct {
	Intent, Context string
	Services        []string
}
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
type Answers struct {
	Status    string
	Relevance []float64
	Usage     Usage
}
type Choice struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}
type ChoiceAnswer struct {
	Choice        string
	Probabilities map[string]float64
}
type Decision struct {
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Question string `json:"question"`
	Tools    []struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	} `json:"tools"`
}
type Selection struct {
	Tool
	Reason string `json:"reason"`
}
type Result struct {
	Status, Summary, Question string
	Tools                     []Selection
	Servers                   []string
	Eligible                  int
	Usage                     Usage
	Decided                   bool // a model decision was produced, even if final validation failed
}

var ErrInvalid = errors.New("invalid routing request")
var ErrModelResponse = errors.New("invalid routing model response")

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
func badResponse(message string, cause error) error {
	return &Error{Kind: ErrModelResponse, Message: message, Cause: cause}
}
