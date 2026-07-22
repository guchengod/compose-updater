#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-dev}"
COMMIT="${COMMIT:-$(git -C "${ROOT}" rev-parse --short HEAD 2>/dev/null || echo none)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
FNPACK="${FNPACK:-fnpack}"
OUT_DIR="${OUT_DIR:-${ROOT}/build/fnos}"

if ! command -v "${FNPACK}" >/dev/null 2>&1 && [ ! -x "${FNPACK}" ]; then
    echo "找不到 fnpack，请安装飞牛官方 fnpack 或设置 FNPACK=/path/to/fnpack" >&2
    exit 1
fi

mkdir -p "${OUT_DIR}"

build_one() {
    local goarch="$1"
    local platform="$2"
    local stage="${ROOT}/build/fnos-stage-${platform}"
    local package_version="${VERSION#v}"

    rm -rf "${stage}"
    mkdir -p "${stage}/app/bin" "${stage}/app/ui/images" "${stage}/cmd" "${stage}/config"
    cp -R "${ROOT}/fnos/package/app/ui/config" "${stage}/app/ui/config"
    cp -R "${ROOT}/fnos/package/cmd/." "${stage}/cmd/"
    cp -R "${ROOT}/fnos/package/config/." "${stage}/config/"
    cp "${ROOT}/fnos/assets/icon_64.png" "${stage}/ICON.PNG"
    cp "${ROOT}/fnos/assets/icon_256.png" "${stage}/ICON_256.PNG"
    cp "${ROOT}/fnos/assets/icon_64.png" "${stage}/app/ui/images/icon_64.png"
    cp "${ROOT}/fnos/assets/icon_256.png" "${stage}/app/ui/images/icon_256.png"
    sed -e "s/@VERSION@/${package_version}/g" -e "s/@PLATFORM@/${platform}/g" \
        "${ROOT}/fnos/package/manifest.template" >"${stage}/manifest"
    chmod 755 "${stage}/cmd/"*

    CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" go build -trimpath \
        -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
        -o "${stage}/app/bin/compose-updater" "${ROOT}/cmd/compose-updater"
    CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" go build -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o "${stage}/app/bin/fnos-manager" "${ROOT}/cmd/fnos-manager"

    (
        cd "${OUT_DIR}"
        "${FNPACK}" build --directory "${stage}"
    )
    local generated="${OUT_DIR}/ComposeUpdater.fpk"
    local target="${OUT_DIR}/ComposeUpdater-${VERSION}-${platform}.fpk"
    if [ ! -f "${generated}" ]; then
        generated="${stage}/ComposeUpdater.fpk"
    fi
    if [ ! -f "${generated}" ]; then
        echo "fnpack 未生成预期的 ComposeUpdater.fpk" >&2
        exit 1
    fi
    mv -f "${generated}" "${target}"
    rm -rf "${stage}"
    echo "已生成 ${target}"
}

build_one amd64 x86
build_one arm64 arm
