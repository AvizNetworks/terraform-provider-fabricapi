#!/bin/bash

set -e

echo "Building Terraform Provider Docker Image..."

# Build the Docker image
docker build -t terraform-fabricapi:latest .

echo "Build complete!"
echo ""
echo "To run the container:"
echo "  docker run -it --rm terraform-fabricapi:latest"
echo ""
echo "To test the provider:"
echo "  docker run -it --rm terraform-fabricapi:latest sh -c 'terraform init && terraform plan'"
