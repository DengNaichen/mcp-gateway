#!/bin/sh
set -eu
PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$PROJECT_DIR"
go build -ldflags="-X gatewaylab/internal/buildinfo.channel=dev" -o bin/dev/gateway ./cmd/gateway
exec ./bin/dev/gateway -data "$PROJECT_DIR/data/dev" "$@"
