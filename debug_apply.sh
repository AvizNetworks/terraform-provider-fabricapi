#!/bin/bash

echo "=== Debugging Terraform Apply Issue ==="
echo ""

echo "Step 1: Testing API directly with curl..."
docker run -it --rm terraform-fabricapi:latest sh -c '
apk add curl > /dev/null 2>&1
echo "Making API call..."
curl -v -X POST http://worker07.air.nvidia.com:29123/fabrics/fab/tenants \
  -H "Content-Type: application/json" \
  -d "{\"tenantName\":\"debug_test\",\"description\":\"Debug test\",\"maxGpusAllowed\":4}" 2>&1 | tee /tmp/api_response.txt

echo ""
echo "=== API Response Analysis ==="
grep "< HTTP" /tmp/api_response.txt
echo ""
echo "Response body:"
tail -1 /tmp/api_response.txt
'

echo ""
echo "Step 2: Running Terraform with debug logging..."
docker run -it --rm \
  -e TF_LOG=DEBUG \
  terraform-fabricapi:latest sh -c '
terraform init > /dev/null 2>&1
echo "Running terraform apply..."
terraform apply -var="tenant_name=debug_test" -auto-approve 2>&1 | grep -A 5 -B 5 "tenant_name"
'

echo ""
echo "=== Debugging Complete ==="
echo ""
echo "Next steps:"
echo "1. Check the API response status code and body above"
echo "2. Look for [DEBUG] lines showing what values are being returned"
echo "3. Check if tenant_name is empty in the response"