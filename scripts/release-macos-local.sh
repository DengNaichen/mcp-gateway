#!/bin/zsh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="$ROOT_DIR/.env.release"

if [[ -f "$ENV_FILE" ]]; then
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line#"${line%%[![:space:]]*}"}"
    [[ -z "$line" || "${line:0:1}" == "#" || "$line" != *"="* ]] && continue
    key="${line%%=*}"
    value="${line#*=}"
    key="${key%"${key##*[![:space:]]}"}"
    value="${value#"${value%%[![:space:]]*}"}"
    value="${value%"${value##*[![:space:]]}"}"
    if [[ ${+parameters[$key]} -eq 0 ]]; then
      export "$key=$value"
    fi
  done < "$ENV_FILE"
fi

PLATFORM="${PLATFORM:-darwin/arm64,darwin/amd64}"
UPLOAD=false
SKIP_NOTARIZE=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --upload)
      UPLOAD=true
      shift
      ;;
    --skip-notarize)
      SKIP_NOTARIZE=true
      shift
      ;;
    --platform)
      PLATFORM="${2:?--platform requires a value}"
      shift 2
      ;;
    -h|--help)
      cat <<EOF
Usage: $0 [--platform darwin/arm64,darwin/amd64] [--skip-notarize] [--upload]

Builds, signs with Developer ID, and notarizes MCP Gateway locally using MyGo.
Environment / .env.release overrides:
  MACOS_SIGNING_IDENTITY    Defaults to the first Developer ID Application in Keychain
  MACOS_NOTARY_PROFILE      Keychain profile from 'xcrun notarytool store-credentials' (default: notary)
  MYGO_UPDATER_PRIVATE_KEY  Ed25519 key (defaults to ~/Library/Application Support/mygo/update-keys/mcp-gateway/mygo-update.key)
EOF
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

cd "$ROOT_DIR"

if [[ -z "${MACOS_SIGNING_IDENTITY:-}" ]]; then
  MACOS_SIGNING_IDENTITY="$(security find-identity -v -p codesigning 2>/dev/null | awk -F '"' '/Developer ID Application:/ {print $2; exit}')"
fi

if [[ -z "${MACOS_SIGNING_IDENTITY:-}" ]]; then
  echo "Error: No Developer ID Application identity found. Set MACOS_SIGNING_IDENTITY explicitly." >&2
  exit 1
fi
export MACOS_SIGNING_IDENTITY

if [[ "$SKIP_NOTARIZE" == false ]]; then
  export MACOS_NOTARY_PROFILE="${MACOS_NOTARY_PROFILE:-notary}"
fi

DEFAULT_KEY_FILE="$HOME/Library/Application Support/mygo/update-keys/mcp-gateway/mygo-update.key"
if [[ -z "${MYGO_UPDATER_PRIVATE_KEY:-}" && -f "$DEFAULT_KEY_FILE" ]]; then
  export MYGO_UPDATER_PRIVATE_KEY="$(cat "$DEFAULT_KEY_FILE")"
fi

if [[ "$UPLOAD" == true && -z "${MYGO_UPDATER_PRIVATE_KEY:-}" ]]; then
  echo "Error: --upload requires MYGO_UPDATER_PRIVATE_KEY (or $DEFAULT_KEY_FILE)." >&2
  exit 1
fi

echo "==> Running Go tests..."
CGO_ENABLED=0 go test -count=1 ./...

build_args=(-platform "$PLATFORM")
if [[ "$SKIP_NOTARIZE" == true ]]; then
  build_args+=(-skip-notarize)
fi
if [[ "$UPLOAD" == true ]]; then
  build_args+=(-upload)
fi

echo "==> Running mygo build (${build_args[*]})..."
echo "    Signing identity: $MACOS_SIGNING_IDENTITY"
if [[ "$SKIP_NOTARIZE" == false ]]; then
  echo "    Notary profile:   ${MACOS_NOTARY_PROFILE}"
fi

node scripts/build-channel.mjs package stable "${build_args[@]}"
