#!/usr/bin/env bash
# Capture the Temporal UI and Jaeger screenshots used in the temporal-purchase-saga-demo post.
# (The worker-crash shots come from ./crash.sh.)
#
# Usage: ./capture.sh <happyId> <shipRetryId> <denyId> <approveId>
#   happyId      completed order with no retries or signals
#   shipRetryId  completed order whose ShipOrderActivity needed a second attempt
#   denyId       order waiting on approveInventory (this script denies it)
#   approveId    order waiting on approveInventory (this script approves it)
# Run ./find-orders.sh to pick IDs. The deny/approve orders get signaled, so they
# can only be used once.
#
# Event ID ranges below are fixed by the workflow's shape: a no-approval path always
# schedules ShipOrderActivity at event 23, and an approval path always starts the
# timer at 23 and receives the signal at 24.
set -euo pipefail

if [[ $# -ne 4 ]]; then
  sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

HAPPY="$1" RETRY="$2" DENY="$3" APPROVE="$4"
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${OUT:-$HERE/../../src/assets/blog/temporal-purchase-saga-demo}"
TEMPORAL_ADDRESS="${TEMPORAL_ADDRESS:-localhost:7233}"
mkdir -p "$OUT"

signal() {
  # --identity keeps the local user@host out of the recorded signal (it shows in the UI).
  temporal --address "$TEMPORAL_ADDRESS" workflow signal \
    --workflow-id "$1" --name approveInventory --identity temporal-cli \
    --input "{\"order_id\":\"$1\",\"approved\":$2}"
}

cd "$HERE"

echo "1. happy path ($HAPPY)"
node timeline.mjs "$HAPPY" "$OUT/01-happy-path.png"

echo "2. retry ($RETRY)"
node events.mjs "$RETRY" "$OUT/02-retry.png" 25 23 24

echo "5. Jaeger trace ($RETRY)"
node jaeger.mjs "$RETRY" "$OUT/05-trace.png"

echo "4a. waiting for signal ($APPROVE)"
node timeline.mjs "$APPROVE" "$OUT/04a-waiting-for-signal.png"

echo "sending signals: deny $DENY, approve $APPROVE"
signal "$DENY" false
signal "$APPROVE" true
sleep 15 # let compensation and shipping finish

echo "3. compensation ($DENY)"
node events.mjs "$DENY" "$OUT/03-compensation.png" 41 24 41

echo "4b. signal received ($APPROVE)"
node events.mjs "$APPROVE" "$OUT/04b-signal-received.png" 29 23 24

echo "done: $OUT"
echo "Update the order IDs in the post's alt text and signal example if they changed."
