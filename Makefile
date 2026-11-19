.PHONY: setup backend desktop package build run dev release clean clean-check

clean-check:
	python3 .agents/skills/git-workflow/scripts/clean-build-artifacts.py

clean:
	python3 .agents/skills/git-workflow/scripts/clean-build-artifacts.py --apply

setup:
	go mod download

backend:
	CGO_ENABLED=0 go build -o bin/dev/gateway ./cmd/gateway
	CGO_ENABLED=0 go build -o bin/dev/gatewayctl ./cmd/gatewayctl
	CGO_ENABLED=0 go build -o bin/dev/gateway-sshlogs ./cmd/sshlogs
	CGO_ENABLED=0 go build -o bin/gateway-hook ./cmd/gateway-hook
	CGO_ENABLED=0 go build -o bin/dev/gateway-hook ./cmd/gateway-hook

desktop:
	node scripts/build-channel.mjs binary dev

package:
	node scripts/build-channel.mjs package dev -skip-dmg

build: backend

run:
	go run ./cmd/gateway

# Local launches and ordinary packages always use the Dev identity.
dev:
	node scripts/build-channel.mjs run dev

# Explicit Stable package, without publishing or installing it.
release:
	node scripts/build-channel.mjs package stable
