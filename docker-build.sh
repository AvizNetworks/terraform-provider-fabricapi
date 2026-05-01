#!/usr/bin/env bash
set -euo pipefail

echo "Building Terraform Provider Docker Image..."

: "${DOCKER_BUILDKIT:=0}"
: "${CACHEBUST:=$(date +%s)}"
: "${PROVIDER_VERSION:=1.0.0}"
: "${IMAGE:=terraform-fabricapi:latest}"

DOCKER_BUILDKIT="${DOCKER_BUILDKIT}" docker build \
  --build-arg "CACHEBUST=${CACHEBUST}" \
  --build-arg "PROVIDER_VERSION=${PROVIDER_VERSION}" \
  -t "${IMAGE}" .

echo "Build complete: ${IMAGE}"

