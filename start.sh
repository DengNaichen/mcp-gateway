#!/bin/sh
set -eu
PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$PROJECT_DIR"
if [ ! -x ./bin/gateway ]; then
  go build -o bin/gateway ./cmd/gateway
fi
exec ./bin/gateway -data "$PROJECT_DIR/data" "$@"
