.PHONY: setup frontend backend build run

setup:
	cd web && npm ci
	go mod download

frontend:
	cd web && npm run build

backend:
	go build -o bin/gateway ./cmd/gateway
	go build -o bin/gatewayctl ./cmd/gatewayctl
	go build -o bin/gateway-sshlogs ./cmd/sshlogs

build: frontend backend

run:
	go run ./cmd/gateway
