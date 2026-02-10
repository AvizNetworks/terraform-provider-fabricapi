#!/bin/bash

set -e

echo "=== Testing Terraform Provider ==="
echo ""

# Initialize Terraform
echo "1. Initializing Terraform..."
docker run -it --rm terraform-fabricapi:latest terraform init
echo ""

# Validate configuration
echo "2. Validating Terraform configuration..."
docker run -it --rm terraform-fabricapi:latest terraform validate
echo ""

# Plan changes
echo "3. Planning Terraform changes..."
docker run -it --rm terraform-fabricapi:latest terraform plan
echo ""

# Apply changes (uncomment when ready to test against real API)
# echo "4. Applying Terraform changes..."
# docker run -it --rm terraform-fabricapi:latest terraform apply -auto-approve
# echo ""

# Show state (uncomment after apply)
# echo "5. Showing Terraform state..."
# docker run -it --rm terraform-fabricapi:latest terraform show
# echo ""

# Destroy resources (uncomment when ready to cleanup)
# echo "6. Destroying resources..."
# docker run -it --rm terraform-fabricapi:latest terraform destroy -auto-approve
# echo ""

echo "=== Testing Complete ==="
