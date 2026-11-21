# Temporal saga demo screenshots

Playwright scripts that produce the images in
`src/content/blog/temporal-purchase-saga-demo.md`: the workflow diagram, Temporal UI
screenshots, and a Jaeger trace. Output goes to
`src/assets/blog/temporal-purchase-saga-demo/`, which Astro optimizes at build time.

This directory has its own `package.json`, so the blog's dependencies and CI install
are unaffected.

## Prerequisites

- A local checkout of [temporal-order-processing](https://github.com/jbirdkerr/temporal-order-processing)
  with Docker running.
- The [Temporal CLI](https://docs.temporal.io/cli) (last tested with 1.8.3).
- Node 22+ and `python3`.

```bash
cd blog/scripts/temporal-purchase-saga-demo
npm ci
npx playwright install chromium
```

## 1. Start the demo stack

```bash
cd temporal-order-processing
docker compose --profile app up --build -d
```

Checkouts at or before commit `88fa360` have a `temporal` health check that probes
`localhost:7233`, but the auto-setup image binds to the container IP, so the check never
passes and the UI, server, and worker never start. On those checkouts, start them
without waiting on it:

```bash
docker compose --profile app up -d --no-deps temporal-ui server worker
curl -s localhost:8080/health   # {"status":"healthy"}
```

The Jaeger trace and worker metrics need the tracing interceptor and worker `/metrics`
endpoint, which also landed after `88fa360`.

## 2. Submit a batch of orders

Outcomes are random: about 15% of orders wait for approval, and the shipping
activity fails about 5% of attempts. Forty orders usually cover every scenario.

```bash
for n in $(seq 1001 1040); do
  curl -s -X POST localhost:8080/orders -H 'Content-Type: application/json' -d "{
    \"id\": \"ORD-$n\", \"customer_id\": \"cust-456\",
    \"items\": [{\"sku\": \"SKU-WIDGET\", \"name\": \"Standard Widget\", \"quantity\": 2, \"price\": 50.0}],
    \"total_amount\": 100.0,
    \"shipping_address\": {\"street\": \"123 Main St\", \"city\": \"Portland\", \"state\": \"OR\",
                           \"zip_code\": \"97201\", \"country\": \"USA\"}
  }" > /dev/null
done
sleep 30
```

## 3. Pick orders and capture

```bash
./find-orders.sh
./capture.sh <happyId> <shipRetryId> <denyId> <approveId>
```

`capture.sh` takes the timeline shots and the Jaeger trace, sends the deny and approve
signals, waits for the workflows to finish, then takes the event-history shots. Signals use
`--identity temporal-cli` so your `user@host` doesn't appear in the screenshots.

## 4. Worker crash

```bash
COMPOSE_DIR=path/to/temporal-order-processing ./crash.sh <newOrderId>
```

`crash.sh` submits an order, runs `docker compose kill worker` while the payment
activity is running, waits for the 30-second start-to-close timeout, captures the
Pending Activities tab, restarts the worker, and captures the retried payment once the
workflow finishes. About 10% of runs end in a random payment decline; the script says
so, and you rerun it with a new order ID.

## 5. Diagram

```bash
node diagram.mjs
```

Renders `workflow.mmd` (adapted from the demo repo's README flowchart) with the locally
installed Mermaid and the blog's JetBrains Mono, on the blog's background color.

## Notes

If the order IDs differ from the ones in the post, update its alt text and its
`temporal workflow signal` example to match.

The individual scripts also work on their own:

```bash
node timeline.mjs <workflowId> <out.png>
node events.mjs <workflowId> <out.png> <fromEventId> <toEventId> [expandIds]
node pending.mjs <workflowId> <out.png>
node jaeger.mjs <workflowId> <out.png>
```

Set `TEMPORAL_UI` (default `http://localhost:8233`), `TEMPORAL_ADDRESS`
(default `localhost:7233`), `JAEGER_UI` (default `http://localhost:16686`), or `API`
(default `http://localhost:8080`) if your stack uses other ports.

## Clean up

```bash
cd temporal-order-processing && docker compose --profile app down
```
