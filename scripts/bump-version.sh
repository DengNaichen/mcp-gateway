#!/bin/zsh
set -euo pipefail

VERSION="${1:-}"

if [[ -z "$VERSION" ]]; then
  echo "Usage: $0 <version>  (e.g. $0 0.2.0 or $0 0.2.0-beta.1)" >&2
  exit 1
fi

if [[ ! "$VERSION" =~ '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' ]]; then
  echo "Error: version must follow semantic versioning (got: $VERSION)" >&2
  exit 1
fi

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

if [[ "$(git branch --show-current)" != "main" ]]; then
  echo "Error: run this script from the main branch." >&2
  exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
  echo "Error: working tree must be clean before releasing." >&2
  exit 1
fi

git fetch origin main
if [[ "$(git rev-parse HEAD)" != "$(git rev-parse origin/main)" ]]; then
  echo "Error: local main is not synchronized with origin/main." >&2
  echo "Run: git merge --ff-only origin/main" >&2
  exit 1
fi

TAG="v$VERSION"
if git rev-parse --verify --quiet "refs/tags/$TAG" >/dev/null; then
  echo "Error: local tag $TAG already exists." >&2
  exit 1
fi
if git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1; then
  echo "Error: remote tag $TAG already exists." >&2
  exit 1
fi

CURRENT_VERSION="$(node -p 'require("./internal/buildinfo/profiles.json").version')"

if [[ "$CURRENT_VERSION" != "$VERSION" ]]; then
  echo "Bumping version $CURRENT_VERSION -> $VERSION"
  NEW_VERSION="$VERSION" node --input-type=module -e '
    import fs from "node:fs";
    const v = process.env.NEW_VERSION;
    const path = "internal/buildinfo/profiles.json";
    const profiles = JSON.parse(fs.readFileSync(path, "utf8"));
    profiles.version = v;
    fs.writeFileSync(path, JSON.stringify(profiles, null, 2) + "\n");
  '

  UPDATED_VERSION="$(node -p 'require("./internal/buildinfo/profiles.json").version')"
  if [[ "$UPDATED_VERSION" != "$VERSION" ]]; then
    echo "Error: version verification failed after update (got: $UPDATED_VERSION)" >&2
    exit 1
  fi

  git add internal/buildinfo/profiles.json
  git commit -m "chore: bump version to $VERSION"
  git push origin HEAD:refs/heads/main
else
  echo "internal/buildinfo/profiles.json is already at $VERSION; tagging HEAD."
fi

git tag "$TAG"
git push origin "$TAG"

echo "Pushed $TAG — monitor workflow with: gh run list -R DengNaichen/mcp-gateway --workflow release.yml --limit 3"
