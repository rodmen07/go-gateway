#!/usr/bin/env bash
# smoke-test.sh <base-url>
#
# Lightweight smoke-test gate used by the canary deploy workflow.
# Exits 0 only when all checks pass; exits 1 on any failure so the workflow
# can trigger an automatic rollback.
#
# Checks:
#   1. GET /health           — must return HTTP 200 with {"status":"ok"}
#   2. GET /health/upstreams — must return HTTP 200 or 502 (degraded is OK
#                              during rollout; a 500/503 from the gateway
#                              itself means the new revision is broken)
#   3. Response latency      — /health must respond in < 3 s
#
# Usage:
#   bash scripts/smoke-test.sh https://go-gateway-xxx-uc.a.run.app
#
# Environment variables:
#   SMOKE_RETRIES   — number of attempts before declaring failure (default 6)
#   SMOKE_INTERVAL  — seconds between retries (default 10)

set -euo pipefail

BASE_URL="${1:-}"
if [ -z "$BASE_URL" ]; then
  echo "::error::Usage: smoke-test.sh <base-url>" >&2
  exit 1
fi
BASE_URL="${BASE_URL%/}"  # strip trailing slash

RETRIES="${SMOKE_RETRIES:-6}"
INTERVAL="${SMOKE_INTERVAL:-10}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

check_http() {
  local label="$1"
  local url="$2"
  local expected_status="$3"
  local max_time="${4:-5}"   # curl --max-time in seconds

  echo "Smoke: ${label} → ${url}"
  local status
  status=$(curl -s -o /tmp/smoke_body -w "%{http_code}" \
    --max-time "${max_time}" \
    --connect-timeout 3 \
    "${url}" || echo "000")

  if [ "$status" != "$expected_status" ]; then
    echo "::error::${label}: expected HTTP ${expected_status}, got ${status}" >&2
    echo "Response body:" >&2
    cat /tmp/smoke_body >&2 || true
    return 1
  fi
  echo "  OK (HTTP ${status})"
  return 0
}

check_json_field() {
  local label="$1"
  local url="$2"
  local field="$3"
  local expected_value="$4"

  local body
  body=$(curl -s --max-time 5 --connect-timeout 3 "${url}" || echo "")
  local actual
  actual=$(echo "$body" | grep -o "\"${field}\":\"[^\"]*\"" | head -1 | cut -d'"' -f4 || echo "")

  if [ "$actual" != "$expected_value" ]; then
    echo "::error::${label}: expected ${field}=${expected_value}, got '${actual}'" >&2
    echo "Response body: ${body}" >&2
    return 1
  fi
  echo "  OK (${field}=${actual})"
  return 0
}

# ---------------------------------------------------------------------------
# Retry loop — new Cloud Run revisions need a few seconds to be reachable.
# ---------------------------------------------------------------------------

for attempt in $(seq 1 "$RETRIES"); do
  echo "--- Smoke attempt ${attempt}/${RETRIES} ---"

    if check_http "/health" "${BASE_URL}/health" "200" "3" && \
      check_json_field "/health body" "${BASE_URL}/health" "status" "ok" && \
      (check_http "/health/upstreams (reachable)" "${BASE_URL}/health/upstreams" "200" "10" || \
      check_http "/health/upstreams (degraded ok)" "${BASE_URL}/health/upstreams" "502" "10"); then
    echo "--- All smoke checks passed ---"
    exit 0
  fi

  if [ "$attempt" -lt "$RETRIES" ]; then
    echo "Retrying in ${INTERVAL}s..."
    sleep "$INTERVAL"
  fi
done

echo "::error::Smoke tests failed after ${RETRIES} attempts against ${BASE_URL}" >&2
exit 1
