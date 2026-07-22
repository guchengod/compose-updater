#!/usr/bin/env sh
set -eu

APP=compose-updater
VERSION=${VERSION:-dev}
COMMIT=${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || printf none)}
BUILD_DATE=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
LDFLAGS="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}"

rm -rf build
mkdir -p build

build() {
  goos=$1
  goarch=$2
  ext=$3
  output="build/${APP}-${goos}-${goarch}${ext}"
  echo "building ${output}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$output" ./cmd/compose-updater
}

build linux amd64 ""
build linux arm64 ""
build darwin amd64 ""
build darwin arm64 ""
build windows amd64 ".exe"
build windows arm64 ".exe"

(
  cd build
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum compose-updater-* > SHA256SUMS
  else
    shasum -a 256 compose-updater-* > SHA256SUMS
  fi
)
