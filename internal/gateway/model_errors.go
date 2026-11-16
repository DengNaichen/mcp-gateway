package gateway

import (
	"errors"

	"gatewaylab/internal/model"
)

func modelError(err error) error {
	var e *model.Error
	if errors.As(err, &e) {
		status := 400
		if errors.Is(e, model.ErrNotFound) {
			status = 404
		}
		if errors.Is(e, model.ErrUpstream) {
			status = 502
		}
		return problem(status, e.Message, e.Cause)
	}
	return err
}
