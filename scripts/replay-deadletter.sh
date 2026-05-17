#!/usr/bin/env bash
# replay-deadletter.sh — Pull messages from the Pub/Sub dead-letter drain
# subscription and republish them to the main ingest topic.
#
# Prerequisites:
#   gcloud CLI authenticated with permission to pull from the subscription
#   and publish to the ingest topic.
#   jq (https://stedolan.github.io/jq/) installed on PATH.
#
# Required env vars:
#   PUBSUB_PROJECT   — GCP project ID
#
# Optional env vars:
#   DEAD_LETTER_SUB  — dead-letter drain subscription name
#                      (default: crm-mutation-ingest-deadletter-drain)
#   INGEST_TOPIC     — main ingest topic name
#                      (default: crm-mutation-ingest)
#   MAX_MESSAGES     — max messages to pull per batch (default: 100)
#   DRY_RUN          — set to "true" to ack without republishing (default: false)

set -euo pipefail

PROJECT="${PUBSUB_PROJECT:?PUBSUB_PROJECT environment variable must be set}"
DEAD_LETTER_SUB="${DEAD_LETTER_SUB:-crm-mutation-ingest-deadletter-drain}"
INGEST_TOPIC="${INGEST_TOPIC:-crm-mutation-ingest}"
MAX_MESSAGES="${MAX_MESSAGES:-100}"
DRY_RUN="${DRY_RUN:-false}"

command -v gcloud >/dev/null 2>&1 || { echo "Error: gcloud CLI is required." >&2; exit 1; }
command -v jq     >/dev/null 2>&1 || { echo "Error: jq is required." >&2; exit 1; }

echo "[replay] project=${PROJECT} sub=${DEAD_LETTER_SUB} topic=${INGEST_TOPIC}"
[ "${DRY_RUN}" = "true" ] && echo "[replay] DRY_RUN=true — messages will be acked but NOT republished."

total=0

while true; do
  raw=$(
    gcloud pubsub subscriptions pull "${DEAD_LETTER_SUB}" \
      --project="${PROJECT}" \
      --limit="${MAX_MESSAGES}" \
      --format=json \
      --auto-ack 2>/dev/null \
    || echo "[]"
  )

  count=$(jq 'if type == "array" then length else 0 end' <<<"${raw}")

  if [ "${count}" -eq 0 ]; then
    echo "[replay] No more messages in dead-letter drain. Total replayed: ${total}"
    break
  fi

  echo "[replay] Pulled ${count} message(s)."

  if [ "${DRY_RUN}" != "true" ]; then
    while IFS= read -r data_b64; do
      if [ -z "${data_b64}" ]; then
        echo "[replay] Warning: empty message data — skipping." >&2
        continue
      fi

      decoded=$(printf '%s' "${data_b64}" | base64 --decode 2>/dev/null) || {
        echo "[replay] Warning: could not base64-decode message data — skipping." >&2
        continue
      }

      gcloud pubsub topics publish "${INGEST_TOPIC}" \
        --project="${PROJECT}" \
        --message="${decoded}" \
        --quiet

      echo "[replay] Published 1 message to ${INGEST_TOPIC}."
    done < <(jq -r '.[] | .message.data // ""' <<<"${raw}")
  fi

  total=$((total + count))
  [ "${count}" -lt "${MAX_MESSAGES}" ] && break
done

echo "[replay] Done. Total messages replayed: ${total}"
