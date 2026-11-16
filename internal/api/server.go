package api

import (
	"gatewaylab/internal/gateway"
	"net/http"
)

type server struct{ *gateway.Gateway }

func Handler(g *gateway.Gateway) http.Handler { return (&server{g}).handler() }
