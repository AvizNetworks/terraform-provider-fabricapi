#!/usr/bin/env bash
# Build and install the fabricapi provider where Terraform expects a local/registry mirror layout.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${VERSION:-1.0.0}"
PLUGIN_NAME="terraform-provider-fabricapi_v${VERSION}"

GOOS="$(go env GOOS)"
GOARCH="$(go env GOARCH)"
case "${GOOS}_${GOARCH}" in
  linux_amd64|linux_arm64|darwin_amd64|darwin_arm64|windows_amd64) ;;
  *)
    echo "Unsupported GOOS/GOARCH: ${GOOS}_${GOARCH} (adjust script if needed)" >&2
    exit 1
    ;;
esac

DEST="${HOME}/.terraform.d/plugins/registry.terraform.io/local/fabricapi/${VERSION}/${GOOS}_${GOARCH}"
mkdir -p "${DEST}"

echo "Building ${PLUGIN_NAME} for ${GOOS}_${GOARCH}..."
(cd "${ROOT}" && CGO_ENABLED=0 go build -o "${DEST}/${PLUGIN_NAME}" .)

chmod +x "${DEST}/${PLUGIN_NAME}"
echo "Installed: ${DEST}/${PLUGIN_NAME}"
