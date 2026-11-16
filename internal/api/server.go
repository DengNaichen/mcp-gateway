package api

import (
	"net/http"

	"gatewaylab/internal/gateway"
)

type server struct{ *gateway.Gateway }

func Handler(g *gateway.Gateway) http.Handler { return (&server{g}).handler() }
