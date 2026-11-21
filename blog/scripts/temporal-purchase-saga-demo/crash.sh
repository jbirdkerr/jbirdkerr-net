#!/usr/bin/env bash
# Kill the worker mid-activity, then show Temporal finishing the workflow after the
# worker comes back. Captures:
#   06a-worker-down.png   Pending Activities while no worker is running (attempt 2, StartToClose timeout)
#   06b-worker-back.png   History: payment's ActivityTaskStarted with attempt 2 and the timeout as last failure
#
# Usage: ./crash.sh <orderId>     (use an order ID that hasn't been submitted yet)
# Takes about a minute: the payment activity's 30s start-to-close timeout has to expire.
set -euo pipefail

if [[ $# -ne 1 ]]; then
  sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
fi

ID="$1"
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${OUT:-$HERE/../../src/assets/blog/temporal-purchase-saga-demo}"
API="${API:-http://localhost:8080}"
TEMPORAL_ADDRESS="${TEMPORAL_ADDRESS:-localhost:7233}"
COMPOSE_DIR="${COMPOSE_DIR:?set COMPOSE_DIR to your temporal-order-processing checkout}"
mkdir -p "$OUT"
cd "$HERE"

describe() { temporal --address "$TEMPORAL_ADDRESS" workflow describe -w "$ID" -o json; }

curl -sf -X POST "$API/orders" -H 'Content-Type: application/json' -d "{
  \"id\": \"$ID\", \"customer_id\": \"cust-456\",
  \"items\": [{\"sku\": \"SKU-WIDGET\", \"name\": \"Standard Widget\", \"quantity\": 2, \"price\": 50.0}],
  \"total_amount\": 100.0,
  \"shipping_address\": {\"street\": \"123 Main St\", \"city\": \"Portland\", \"state\": \"OR\",
                         \"zip_code\": \"97201\", \"country\": \"USA\"}
}" > /dev/null
echo "submitted $ID; waiting for the payment activity to start"

until describe | python3 -c '
import json, sys
pa = json.load(sys.stdin).get("pendingActivities", [])
sys.exit(0 if any(p["activityType"]["name"] == "ProcessPaymentActivity"
                  and p.get("state", "").endswith("STARTED") for p in pa) else 1)'; do
  sleep 0.2
done

(cd "$COMPOSE_DIR" && docker compose kill worker > /dev/null 2>&1)
echo "worker killed mid-payment at $(date +%T); waiting for the 30s start-to-close timeout"

until describe | python3 -c '
import json, sys
pa = json.load(sys.stdin).get("pendingActivities", [])
sys.exit(0 if pa and pa[0].get("attempt", 1) >= 2 else 1)'; do
  sleep 2
done
node pending.mjs "$ID" "$OUT/06a-worker-down.png"

(cd "$COMPOSE_DIR" && docker compose start worker > /dev/null 2>&1)
echo "worker restarted at $(date +%T); waiting for the workflow to finish"

until describe | python3 -c '
import json, sys
sys.exit(0 if json.load(sys.stdin)["workflowExecutionInfo"]["status"].endswith("COMPLETED") else 1)'; do
  sleep 2
done

status=$(temporal --address "$TEMPORAL_ADDRESS" workflow show -w "$ID" -o json | python3 -c '
import base64, json, sys
last = json.load(sys.stdin)["events"][-1]["workflowExecutionCompletedEventAttributes"]
print(json.loads(base64.b64decode(last["result"]["payloads"][0]["data"]))["status"])')
if [[ "$status" != "delivered" ]]; then
  echo "$ID finished as '$status' (the demo randomly declines ~10% of payments); rerun with a new order ID" >&2
  exit 1
fi

# Payment is always scheduled at event 11 and started at 12 on this path.
node events.mjs "$ID" "$OUT/06b-worker-back.png" 13 11 12
echo "done: $OUT"
