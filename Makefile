.PHONY: setup frontend backend desktop package build run

setup:
	cd web && npm ci
	go mod download

frontend:
	cd web && npm run build

backend:
	CGO_ENABLED=0 go build -o bin/gateway ./cmd/gateway
	CGO_ENABLED=0 go build -o bin/gatewayctl ./cmd/gatewayctl
	CGO_ENABLED=0 go build -o bin/gateway-sshlogs ./cmd/sshlogs

desktop: frontend
	CGO_ENABLED=0 go build -o bin/gateway-desktop ./cmd/gateway-desktop

package: frontend
	CGO_ENABLED=0 go tool mygo build -skip-dmg

build: frontend backend

run:
	go run ./cmd/gateway
