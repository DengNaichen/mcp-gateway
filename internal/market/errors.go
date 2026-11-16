package market

import "gatewaylab/internal/fault"

func invalid(message string) error { return fault.New(fault.Invalid, message, nil) }
