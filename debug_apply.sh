#!/bin/bash

echo "=== Debugging Terraform Apply Issue ==="
echo ""

echo "Step 1: Testing API directly with curl..."
set -euo pipefail

if ! command -v curl >/dev/null 2>&1; then
  echo "Error: curl is required but was not found in PATH." >&2
  exit 1
fi

API_URL="${API_URL:-http://worker07.air.nvidia.com:29123/fabrics/fab/tenants}"
TMP_FILE="${TMP_FILE:-/tmp/fabricapi_debug_api_response.txt}"

echo "Making API call to: ${API_URL}"
curl -v -X POST "${API_URL}" \
  -H "Content-Type: application/json" \
  -d "{\"tenantName\":\"debug_test\",\"description\":\"Debug test\",\"maxGpusAllowed\":4}" 2>&1 | tee "${TMP_FILE}"

echo ""
echo "=== API Response Analysis ==="
grep "< HTTP" "${TMP_FILE}" || true
echo ""
echo "Response body (last line):"
tail -1 "${TMP_FILE}" || true

echo ""
echo "Step 2: Running Terraform with debug logging..."
if ! command -v terraform >/dev/null 2>&1; then
  echo "Error: terraform is required but was not found in PATH." >&2
  exit 1
fi

./build.sh >/dev/null

(cd examples && TF_LOG=DEBUG terraform init >/dev/null 2>&1)
echo "Running terraform apply..."
(cd examples && TF_LOG=DEBUG terraform apply -var="tenant_name=debug_test" -auto-approve 2>&1 | grep -A 5 -B 5 "tenant_name") || true

echo ""
echo "=== Debugging Complete ==="
echo ""
echo "Next steps:"
echo "1. Check the API response status code and body above"
echo "2. Look for [DEBUG] lines showing what values are being returned"
echo "3. Check if tenant_name is empty in the response"