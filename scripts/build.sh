#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
target_arch="${1:-arm64}"
case "$target_arch" in
  amd64|arm64|arm-7) ;;
  *) echo 'Usage: bash scripts/build.sh [amd64|arm64|arm-7]' >&2; exit 2 ;;
esac

# The official server v3.1.1 Docker image uses these same cross-build images.
# They run on amd64 and include the cross C compiler required by Go plugins.
go_version="$(tr -d '[:space:]' < compatibility/GO_VERSION)"
image="gotify/build:${go_version}-linux-${target_arch}"
output="cloud-mail-webhook-gotify-v3.1.1-linux-${target_arch}.so"
mkdir -p build .build-cache/gomod

docker run --rm --platform linux/amd64 \
  --user "$(id -u):$(id -g)" \
  -e GOCACHE=/tmp/go-build -e GOPATH=/go -e GOTOOLCHAIN=local \
  -v "$PWD/.build-cache/gomod:/go/pkg/mod:z" \
  -v "$PWD:/proj:z" -w /proj "$image" \
  go build -mod=readonly -a -installsuffix cgo -ldflags '-w -s' \
  -buildmode=plugin -o "build/$output" .

printf '\nBuilt: build/%s\n' "$output"
echo 'Only install into an official Gotify 3.1.1 container with the matching CPU architecture.'
