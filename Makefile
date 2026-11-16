.PHONY: setup frontend backend desktop package build run dev release

setup:
	cd web && npm ci
	go mod download

frontend:
	cd web && npm run build

backend:
	CGO_ENABLED=0 go build -o bin/dev/gateway ./cmd/gateway
	CGO_ENABLED=0 go build -o bin/dev/gatewayctl ./cmd/gatewayctl
	CGO_ENABLED=0 go build -o bin/dev/gateway-sshlogs ./cmd/sshlogs
	CGO_ENABLED=0 go build -o bin/gateway-hook ./cmd/gateway-hook
	CGO_ENABLED=0 go build -o bin/dev/gateway-hook ./cmd/gateway-hook

desktop: frontend
	node scripts/build-channel.mjs binary dev

package: frontend
	node scripts/build-channel.mjs package dev -skip-dmg

build: frontend backend

run:
	go run ./cmd/gateway

# Local launches and ordinary packages always use the Dev identity.
dev:
	node scripts/build-channel.mjs run dev

# Explicit Stable package, without publishing or installing it.
release: frontend
	node scripts/build-channel.mjs package stable
