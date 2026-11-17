#!/bin/zsh
set -euo pipefail

ROOT_DIR="$(cd -P "$(dirname "$0")" && cd ../../../.. && pwd -P)"

case "$(uname -m)" in
  arm64) HOST_ARCH="arm64" ;;
  x86_64) HOST_ARCH="amd64" ;;
  *) HOST_ARCH="$(uname -m)" ;;
esac

PLATFORM="darwin/${HOST_ARCH}"
INCLUDE_DMG=false
RUN_TESTS=false
INSTALL_APP=false
OPEN_APP=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --platform)
      PLATFORM="${2:?--platform requires a value}"
      shift 2
      ;;
    --dmg)
      INCLUDE_DMG=true
      shift
      ;;
    --test)
      RUN_TESTS=true
      shift
      ;;
    --install)
      INSTALL_APP=true
      shift
      ;;
    --open)
      OPEN_APP=true
      shift
      ;;
    -h|--help)
      cat <<EOF
Usage: $0 [--platform darwin/arm64] [--dmg] [--test] [--install] [--open]

Packages the local Dev macOS app bundle using scripts/build-channel.mjs.
  --platform <p>  Target platform (default: darwin/${HOST_ARCH})
  --dmg           Also build a .dmg disk image (default passes -skip-dmg)
  --test          Run 'CGO_ENABLED=0 go test -count=1 ./...' before packaging
  --install       Copy 'MCP Gateway Dev.app' into /Applications (single target only)
  --open          Launch the packaged (or installed) 'MCP Gateway Dev.app'
EOF
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

if [[ ("$INSTALL_APP" == true || "$OPEN_APP" == true) && "$PLATFORM" == *","* ]]; then
  echo "Error: --install and --open require a single target --platform (got: $PLATFORM)" >&2
  exit 1
fi

cd "$ROOT_DIR"

if [[ "$RUN_TESTS" == true ]]; then
  echo "==> Running Go tests..."
  CGO_ENABLED=0 go test -count=1 ./...
fi

build_args=(-platform "$PLATFORM")
if [[ "$INCLUDE_DMG" == false ]]; then
  build_args+=(-skip-dmg)
fi

echo "==> Packaging Dev app (${build_args[*]})..."
node scripts/build-channel.mjs package dev "${build_args[@]}"

if [[ "$PLATFORM" == *","* ]]; then
  echo "==> Built Dev artifacts under: $ROOT_DIR/build/dev"
  exit 0
fi

PLATFORM_DIR="${PLATFORM/\//-}"
APP_PATH="$ROOT_DIR/build/dev/$PLATFORM_DIR/MCP Gateway Dev.app"
if [[ ! -d "$APP_PATH" ]]; then
  echo "Error: expected app bundle not found at $APP_PATH" >&2
  exit 1
fi

echo "==> Built Dev app: $APP_PATH"
TARGET_APP="$APP_PATH"

if [[ "$INSTALL_APP" == true ]]; then
  DEST_APP="/Applications/MCP Gateway Dev.app"
  echo "==> Installing to $DEST_APP..."
  rm -rf "$DEST_APP"
  ditto "$APP_PATH" "$DEST_APP"
  TARGET_APP="$DEST_APP"
fi

if [[ "$OPEN_APP" == true ]]; then
  if [[ -f "$ROOT_DIR/.env.worktree" ]]; then
    echo "Note: launching .app directly uses default Dev port 8789; for isolated worktree ports use: node .agents/skills/git-workflow/scripts/worktree-runtime.mjs run desktop"
  fi
  echo "==> Opening $TARGET_APP..."
  open "$TARGET_APP"
fi
