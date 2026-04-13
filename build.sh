#!/bin/bash

set -e

echo "Building Terraform Provider Docker Image..."

# This repo should build on hosts that don't have Docker buildx installed.
# If you *do* have BuildKit/buildx, you can still opt in for faster builds:
#   DOCKER_BUILDKIT=1 ./build.sh
: "${DOCKER_BUILDKIT:=0}"
# Default: unique each run so Docker does not reuse a stale Go binary after provider edits.
: "${CACHEBUST:=$(date +%s)}"
DOCKER_BUILDKIT="${DOCKER_BUILDKIT}" docker build \
  --build-arg "CACHEBUST=${CACHEBUST}" \
  -t terraform-fabricapi:latest .

echo "Build complete!"
echo ""
echo "To run the container:"
echo "  docker run -it --rm terraform-fabricapi:latest"
echo ""
echo "To test the provider:"
echo "  docker run -it --rm terraform-fabricapi:latest sh -c 'terraform init && terraform plan'"
