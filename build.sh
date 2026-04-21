#!/bin/bash

set -e

echo "Building & installing Terraform provider locally..."

if ! command -v go >/dev/null 2>&1; then
  echo "Error: go is required but was not found in PATH." >&2
  exit 1
fi

./scripts/install-local-provider.sh

echo ""
echo "Build complete!"
echo ""
echo "Next (example root in this repo):"
echo "  cd examples"
echo "  terraform init"
echo "  terraform plan"
