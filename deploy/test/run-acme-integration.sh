#!/usr/bin/env bash
# Stand up Pebble + challtestsrv, run the acme_integration test against them, tear
# down. Proves real ACME DNS-01 issuance with no public exposure / no real domain.
#
# Usage: deploy/test/run-acme-integration.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
compose="$here/pebble-compose.yml"

cleanup() { docker compose -f "$compose" down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "==> starting Pebble + challtestsrv"
docker compose -f "$compose" up -d

# Wait for the ACME directory to answer.
echo "==> waiting for Pebble"
for i in $(seq 1 30); do
  if curl -ksf https://localhost:14000/dir >/dev/null 2>&1; then break; fi
  sleep 1
done

# Extract Pebble's minica so lego trusts the ACME endpoint's TLS.
ca="$(mktemp)"
docker compose -f "$compose" cp pebble:/test/certs/pebble.minica.pem "$ca"

echo "==> running acme_integration test"
cd "$repo"
GOWORK=off GOFLAGS= \
  PEBBLE_DIR="https://localhost:14000/dir" \
  PEBBLE_CA="$ca" \
  CHALLTESTSRV_URL="http://localhost:8055" \
  go test -tags acme_integration -run TestPebbleDNS01Issuance -v ./internal/acme/...