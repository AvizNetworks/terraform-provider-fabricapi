#!/bin/bash

set -e

echo "Building Terraform Provider Docker Image..."

# This repo should build on hosts that don't have Docker buildx installed.
# If you *do* have BuildKit/buildx, you can still opt in for faster builds:
#   DOCKER_BUILDKIT=1 ./build.sh
: "${DOCKER_BUILDKIT:=0}"
DOCKER_BUILDKIT="${DOCKER_BUILDKIT}" docker build -t terraform-fabricapi:latest .

echo "Build complete!"
echo ""
echo "After changing internal/provider/*.go, rebuild without cache so the image embeds the new binary:"
echo "  docker build --no-cache -t terraform-fabricapi:latest ."
echo ""
echo "To run the container (interactive shell; cwd is /workspace — all examples are here):"
echo "  docker run -it --rm terraform-fabricapi:latest"
echo "  # then: cd decoupled/01-tenant | decoupled/02-servers | decoupled/03-vpcpeering"
echo ""
echo "To test the provider (from bundled examples root):"
echo "  docker run -it --rm terraform-fabricapi:latest sh -c 'terraform init && terraform plan'"
