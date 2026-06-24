#!/usr/bin/env bash
# canary-metrics-gate.sh <service-name> <region>
#
# Fails deployment promotion when canary error/latency metrics exceed threshold.
#
# Env vars:
#   GCP_PROJECT_ID
#   CANARY_MAX_5XX_RATE_PCT (default: 1.0)
#   CANARY_MAX_P99_MS (default: 2500)
#   CANARY_MIN_REQUESTS (default: 5)

set -euo pipefail

SERVICE_NAME="${1:-}"
REGION="${2:-}"
PROJECT_ID="${GCP_PROJECT_ID:-}"
MAX_5XX_RATE_PCT="${CANARY_MAX_5XX_RATE_PCT:-1.0}"
MAX_P99_MS="${CANARY_MAX_P99_MS:-2500}"
MIN_REQUESTS="${CANARY_MIN_REQUESTS:-5}"

if [ -z "$SERVICE_NAME" ] || [ -z "$REGION" ] || [ -z "$PROJECT_ID" ]; then
  echo "::error::Usage: canary-metrics-gate.sh <service-name> <region> (requires GCP_PROJECT_ID)"
  exit 1
fi

FILTER_BASE="resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${SERVICE_NAME}\" AND resource.labels.location=\"${REGION}\""

total_requests=$(gcloud monitoring time-series list \
  --project="$PROJECT_ID" \
  --filter="metric.type=\"run.googleapis.com/request_count\" AND ${FILTER_BASE}" \
  --aggregation.alignment-period=300s \
  --aggregation.per-series-aligner=ALIGN_SUM \
  --aggregation.cross-series-reducer=REDUCE_SUM \
  --format='value(points[0].value.int64Value)' | head -n1)

total_requests=${total_requests:-0}
if ! [[ "$total_requests" =~ ^[0-9]+$ ]]; then
  total_requests=0
fi

if [ "$total_requests" -lt "$MIN_REQUESTS" ]; then
  echo "::error::Canary metrics gate blocked promotion: only ${total_requests} requests in window (minimum ${MIN_REQUESTS})."
  exit 1
fi

error_requests=$(gcloud monitoring time-series list \
  --project="$PROJECT_ID" \
  --filter="metric.type=\"run.googleapis.com/request_count\" AND metric.labels.response_code_class=\"500\" AND ${FILTER_BASE}" \
  --aggregation.alignment-period=300s \
  --aggregation.per-series-aligner=ALIGN_SUM \
  --aggregation.cross-series-reducer=REDUCE_SUM \
  --format='value(points[0].value.int64Value)' | head -n1)

error_requests=${error_requests:-0}
if ! [[ "$error_requests" =~ ^[0-9]+$ ]]; then
  error_requests=0
fi

p99_seconds=$(gcloud monitoring time-series list \
  --project="$PROJECT_ID" \
  --filter="metric.type=\"run.googleapis.com/request_latencies\" AND ${FILTER_BASE}" \
  --aggregation.alignment-period=300s \
  --aggregation.per-series-aligner=ALIGN_PERCENTILE_99 \
  --aggregation.cross-series-reducer=REDUCE_MAX \
  --format='value(points[0].value.doubleValue)' | head -n1)

p99_seconds=${p99_seconds:-0}

export ERR="$error_requests" TOTAL="$total_requests" P99_S="$p99_seconds"
error_rate_pct=$(python - <<'PY'
import os
err = float(os.environ['ERR'])
total = float(os.environ['TOTAL'])
print((err / total) * 100.0 if total > 0 else 100.0)
PY
)

p99_ms=$(python - <<'PY'
import os
s = float(os.environ['P99_S'] or 0)
print(s * 1000.0)
PY
)

echo "Canary metrics: total=${total_requests} errors=${error_requests} error_rate_pct=${error_rate_pct} p99_ms=${p99_ms}"

export ACTUAL_ERR_PCT="$error_rate_pct" MAX_ERR_PCT="$MAX_5XX_RATE_PCT" ACTUAL_P99_MS="$p99_ms" MAX_P99_MS="$MAX_P99_MS"
python - <<'PY'
import os
actual_err = float(os.environ['ACTUAL_ERR_PCT'])
max_err = float(os.environ['MAX_ERR_PCT'])
actual_p99 = float(os.environ['ACTUAL_P99_MS'])
max_p99 = float(os.environ['MAX_P99_MS'])

failed = False
if actual_err > max_err:
    print(f"::error::Canary 5xx rate {actual_err:.2f}% exceeds threshold {max_err:.2f}%")
    failed = True
if actual_p99 > max_p99:
    print(f"::error::Canary p99 {actual_p99:.2f}ms exceeds threshold {max_p99:.2f}ms")
    failed = True

if failed:
    raise SystemExit(1)

print("Canary metrics gate passed.")
PY
