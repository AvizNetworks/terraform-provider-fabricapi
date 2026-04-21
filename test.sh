#!/bin/bash

set -e

echo "=== Testing Terraform Provider ==="
echo ""

if ! command -v terraform >/dev/null 2>&1; then
  echo "Error: terraform is required but was not found in PATH." >&2
  exit 1
fi

# Ensure provider is installed for local runs
./build.sh >/dev/null

# Initialize Terraform
echo "1. Initializing Terraform..."
(cd examples && terraform init)
echo ""

# Validate configuration
echo "2. Validating Terraform configuration..."
(cd examples && terraform validate)
echo ""

# Plan changes
echo "3. Planning Terraform changes..."
(cd examples && terraform plan)
echo ""

# Apply changes (uncomment when ready to test against real API)
# echo "4. Applying Terraform changes..."
# (cd examples && terraform apply -auto-approve)
# echo ""

# Show state (uncomment after apply)
# echo "5. Showing Terraform state..."
# (cd examples && terraform show)
# echo ""

# Destroy resources (uncomment when ready to cleanup)
# echo "6. Destroying resources..."
# (cd examples && terraform destroy -auto-approve)
# echo ""

echo "=== Testing Complete ==="
