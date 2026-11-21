#!/usr/bin/env bash
# List demo workflows by the scenario they can illustrate. Outcomes in the demo are
# random, so submit a batch of orders first (see README.md), then pick IDs from here.
#
# Usage: ./find-orders.sh
set -euo pipefail

TEMPORAL_ADDRESS="${TEMPORAL_ADDRESS:-localhost:7233}"

temporal --address "$TEMPORAL_ADDRESS" workflow list --limit 200 -o json \
  | python3 -c 'import json, sys; [print(w["execution"]["workflowId"]) for w in json.load(sys.stdin)]' \
  | sort \
  | while read -r id; do
      temporal --address "$TEMPORAL_ADDRESS" workflow show -w "$id" -o json | python3 -c '
import json, sys
wid = sys.argv[1]
events = json.load(sys.stdin)["events"]
types = [e["eventType"] for e in events]
scheduled = {e["eventId"]: e["activityTaskScheduledEventAttributes"]["activityType"]["name"]
             for e in events if "activityTaskScheduledEventAttributes" in e}
retried = [scheduled[e["activityTaskStartedEventAttributes"]["scheduledEventId"]]
           for e in events
           if e.get("activityTaskStartedEventAttributes", {}).get("attempt", 1) > 1]
signaled = "EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED" in types
running = types[-1] == "EVENT_TYPE_TIMER_STARTED"
completed = "EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED" in types
acts = set(scheduled.values())

if running and not signaled:
    print(f"{wid}\twaiting-for-signal (use for approve/deny)")
elif retried:
    print(wid + "\tretry: " + ", ".join(retried))
elif completed and "SendNotificationActivity" in acts and not signaled:
    print(f"{wid}\thappy-path")
' "$id"
    done
