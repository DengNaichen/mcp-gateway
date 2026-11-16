package gateway

import (
	"context"
	"errors"
	"os"

	"gatewaylab/internal/model"
	"gatewaylab/internal/routing"
)

const routingProtocol = routing.Protocol

type routingOptions = routing.Options

func loadRoutingOptions() (routing.Options, error) {
	return routing.ParseOptions(os.Getenv("GATEWAY_ROUTING"))
}

type routingModel struct {
	settings model.Settings
	key      string
}

var _ routing.Client = routingModel{}

func (m routingModel) Decisions(ctx context.Context, input string, instructions []string) (routing.Answers, error) {
	a, err := model.AskDecisions(ctx, m.settings, m.key, input, instructions)
	return routing.Answers{Status: a.Status, Relevance: a.Relevance, Usage: routing.Usage(a.Usage)}, err
}
func (m routingModel) Choose(ctx context.Context, input, instructions string, choices []routing.Choice) (routing.ChoiceAnswer, routing.Usage, error) {
	cs := make([]model.Choice, len(choices))
	for i, c := range choices {
		cs[i] = model.Choice(c)
	}
	a, usage, err := model.AskChoice(ctx, m.settings, m.key, input, instructions, cs)
	return routing.ChoiceAnswer{Choice: a.Choice, Probabilities: a.Probabilities}, routing.Usage(usage), err
}
func routingRequest(req DiscoverRequest) routing.Request {
	return routing.Request{Intent: req.Intent, Context: req.Context, Services: req.Services}
}
func routingError(err error) error {
	var e *routing.Error
	if errors.As(err, &e) {
		status := 502
		if errors.Is(e, routing.ErrInvalid) {
			status = 400
		}
		return problem(status, e.Message, e.Cause)
	}
	return modelError(err)
}
