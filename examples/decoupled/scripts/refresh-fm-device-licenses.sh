#!/usr/bin/env bash
# Refresh fabric device rows in FM Postgres so license-gated API calls succeed.
# Run immediately before terraform apply for 02-servers / 03-vpcpeering (or any
# FM write that calls checkFabricDevicesLicense). Quartz may revert LICENSED
# after a few minutes — re-run if you see HTTP 403 "not in LICENSED state".
#
# Usage:
#   ./examples/decoupled/scripts/refresh-fm-device-licenses.sh
#   DOCKER="sudo docker" ./examples/decoupled/scripts/refresh-fm-device-licenses.sh
#
# Override defaults via env:
#   FM_DB_CONTAINER=ones-fm-db FM_DB_NAME=ones_fm DEVICE_IPS="10.0.0.1,10.0.0.2" ...

set -euo pipefail

DOCKER="${DOCKER:-docker}"
FM_DB_CONTAINER="${FM_DB_CONTAINER:-ones-fm-db}"
FM_DB_USER="${FM_DB_USER:-postgres}"
FM_DB_NAME="${FM_DB_NAME:-ones_fm}"

# Default: lab fabric device IPs (edit for your testbed)
DEVICE_IPS="${DEVICE_IPS:-192.168.122.8,192.168.122.9,192.168.122.10,192.168.122.11,192.168.122.12,192.168.122.13,192.168.122.14,192.168.122.15,192.168.122.16}"

IPS_SQL=$(echo "$DEVICE_IPS" | tr ',' '\n' | sed "s/^/'/;s/$/'/" | paste -sd, -)

$DOCKER exec -i "$FM_DB_CONTAINER" psql -U "$FM_DB_USER" -d "$FM_DB_NAME" -c "
UPDATE public.device_license
   SET license_state = 'LICENSED',
       features      = 'FM',
       updated_at    = NOW()
 WHERE device_ip IN ($IPS_SQL);
SELECT device_ip, license_state FROM public.device_license
 WHERE device_ip IN ($IPS_SQL) ORDER BY device_ip;
"
