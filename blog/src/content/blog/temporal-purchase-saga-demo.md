---
title: "Durable Payments on a Laptop: A Temporal Workflow Demo"
description: A Go order-processing saga built on Temporal, with retries, compensation, and observability, and why I'm not hosting it.
pubDate: 2026-10-31
tags: [temporal, go, workflows, payments]
draft: true
---

## The problem, then and now

I built PCI-compliant payment portals before services like Stripe existed. Back then, "reliable payments" meant a pile of hand-rolled machinery: a status column on an orders table, a retry table, a cron job that swept for payments stuck in limbo, and a reconciliation report someone had to read on Monday morning. Every one of those pieces existed to answer a single question: *what happened to this payment, and what do we do next?*

That question hasn't gone away. What has changed is that there are now tools that make the answer a property of the platform instead of something every team reinvents. This post is about one of them, Temporal, and a small payment workflow I built to show it off.

I have built Temporal proof-of-concept demos before, mostly to show the durability and observability story to people who hadn't seen it. This one is the version I can share publicly: a standard payment flow, written in Go, that you can run on your own machine with Docker.

## What is Temporal?

Temporal is an open-source platform for [durable execution](https://temporal.io/blog/what-is-durable-execution). You write a multi-step process as ordinary code (here, Go), and Temporal records each step's progress so the process survives crashes, restarts, and outages. Failed steps are retried according to policies you declare, and a workflow can wait hours or days without holding a process open.

The moving parts are small: a Temporal server that stores the history, and workers, which are your own processes that run the workflow and activity code. If you want the full picture, the [Temporal documentation](https://docs.temporal.io/) is a good place to start.

## Why a payment workflow

Payments are a good demo domain because everyone already understands them. Nobody needs a primer on what "authorize, then capture" means, so the reader's attention can go to the interesting part: what happens when things go wrong halfway through.

A payment also has the right shape for this kind of tool. It is a sequence of steps across systems you don't control, each step can fail or time out, and some steps have real-world side effects that you must not repeat or forget to undo.

## What I'm not hosting, and why

There is no live demo link in this post, on purpose. Running a Temporal server plus workers plus a database for the sake of a portfolio piece would add recurring cost and upkeep for something that is better experienced locally. The whole thing starts with one command, and I'd rather you run it yourself and break it than watch me click around a hosted copy.

```bash
docker compose --profile app up --build
```

The code is at [jbirdkerr/temporal-order-processing](https://github.com/jbirdkerr/temporal-order-processing).

## The workflow

The repo implements an order-processing saga, with payment as one step among several. Each order moves through six steps, and if a later step fails, the earlier ones are undone.

1. **Fraud check.** A flagged order fails immediately.
2. **Process payment.** A decline, or retries running out, fails the order.
3. **Reserve inventory.** Out of stock triggers a refund; some orders pause for manual approval first.
4. **Ship order.** A carrier error after retries releases the inventory and refunds the payment.
5. **Confirm delivery.**
6. **Send notification.**

[DIAGRAM: the repo README already has a flowchart of this flow; export it as an image or redraw it, with the compensation paths dashed]

Each step is an *activity*, meaning the code that actually touches the outside world. The *workflow* is the orchestration code that decides the order and what to do on failure. The key idea is that the workflow's progress is recorded as an event history, so if a worker process dies mid-payment, another worker replays the history and continues exactly where the first one stopped.

Here is the shape of the retry configuration. [ILLUSTRATIVE: replace with the real snippet from the repo]

```go title="order.go" github="https://github.com/jbirdkerr/temporal-order-processing/blob/main/internal/workflow/order.go#L25-L45"
logger.Info("Starting fraud check", "orderID", order.ID)

fraudCheckCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
    StartToCloseTimeout: 30 * time.Second,
    RetryPolicy: &temporal.RetryPolicy{
        InitialInterval:    time.Second,
        BackoffCoefficient: 2.0,
        MaximumInterval:    10 * time.Second,
        MaximumAttempts:    3,
    },
})

var fraudCheckResult *activity.FraudCheckResult
err := workflow.ExecuteActivity(
    fraudCheckCtx,
    activity.CheckFraudActivity,
    order,
).Get(ctx, &fraudCheckResult)
```

The detail worth noticing is `NonRetryableErrorTypes`. A network timeout is worth retrying; a declined card is not. Encoding that distinction in one place, rather than scattering it across handlers, is a large part of the appeal.

Idempotency is handled by workflow ID deduplication, so submitting the same order twice doesn't process it twice. [CONFIRM: the workflow ID is derived from the order ID]

Compensation follows the saga pattern: when a step succeeds, you register how to undo it, and if something later fails, those undo steps run in reverse order. [ADJUST: describe how the repo implements this, for example with defer or a compensation list]

```go title="order.go" github="https://github.com/jbirdkerr/temporal-order-processing/blob/main/internal/workflow/order.go#L126-L142"

if err != nil {
    logger.Error("Inventory reservation failed", "orderID", order.ID, "error", err)

    // Compensation: Refund payment
    logger.Info("Compensating: Refunding payment", "orderID", order.ID, "paymentID", result.PaymentID)
    compCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
        StartToCloseTimeout: 30 * time.Second,
        RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5},
    })
    if refundErr := workflow.ExecuteActivity(compCtx, activity.RefundPaymentActivity, result.PaymentID).Get(ctx, nil); refundErr != nil {
        logger.Error("Failed to refund payment during compensation", "orderID", order.ID, "error", refundErr)
    }

    result.Status = domain.OrderFailed
    result.FailureReason = "Inventory unavailable: " + err.Error()
    return result, nil
}

```

## Durability, shown

This is the part prose can't do justice to, so here are four moments from the Temporal UI.

### 1. The happy path

A clean run, start to finish.

[SCREENSHOT: workflow event history for a successful order, with all activities completed]

Each row is a recorded event. Nothing here is a log line someone chose to write; it is the system's own record of what happened and in what order.

### 2. A failure and a retry

I force a transient failure in the payment activity, then let it succeed on a later attempt.

[SCREENSHOT: the failed activity attempt with its error, followed by the successful retry]

[FILL IN: the command or flag used to trigger the failure locally]

The workflow code didn't change and I didn't write any retry loop. The policy declared above did the work, and the history shows each attempt.

### 3. Compensation

Now a failure that can't be retried away, after money has already moved: the item is out of stock, so the workflow refunds the payment.

[SCREENSHOT: workflow history showing the failure followed by the refund activity]

This is the moment I most wanted to capture. In the old world, this case lived in a runbook and a Monday-morning report. Here it is a visible, ordered part of the workflow's history.

[OPTIONAL: a short GIF of the Temporal UI during a retry]

### 4. Waiting on a human

Some orders need manual approval after inventory is reserved. The workflow pauses and waits for an `approveInventory` signal, with a 24-hour timeout. If the approval is denied or the timer expires, it releases the inventory and refunds the payment.

[SCREENSHOT: the workflow waiting on the signal, then the signal arriving in the history]

[FILL IN: the command used to send the signal]

## Observability

The thing I keep coming back to when I demo Temporal is how much you get without instrumenting anything. For any workflow you can see its current state, every attempt of every activity, the inputs and outputs, and how long each step took. When someone asks "what happened to payment X?", you open one page instead of correlating logs across services.

The repo goes further than the Temporal UI. It exposes Prometheus metrics for each activity and state transition, and OpenTelemetry traces over gRPC that Jaeger, Grafana Tempo, or an OTel Collector can consume. The local stack includes Prometheus, Jaeger, and Grafana, so you can look at the same order three ways: workflow history, metrics, and a trace waterfall.

[SCREENSHOT: a Jaeger trace or Grafana dashboard for the same order]

[SCREENSHOT: a view of a running or stuck workflow showing pending activity and attempt count]

## Honest trade-offs

This is the section the demo can't hide from.

**Operational cost.** Temporal is another system to run. Self-hosting means a server, a persistence store, and upgrades. Temporal Cloud removes that burden for a fee. [CHECK CURRENT PRICING before stating numbers, and add your own estimate of time spent maintaining a self-hosted setup]

**Learning cost.** Workflow code must be deterministic, because it gets replayed. That constraint is manageable but real, and it trips people up. [FILL IN: anything that bit you while building this]

**When you don't need it.** If your payment flow is a single call to a managed provider like Stripe, with the provider handling retries and idempotency, adding a workflow engine is complexity for its own sake. Temporal earns its keep when you have multiple steps across multiple systems, long-running processes, or compensation logic you can't push to a provider.

The honest summary: the tool solves a real problem, but you should have that problem first.

## What this demo is not

This is a simulation. It does not handle real card data, tokenization, or any of what makes a system PCI-scoped, and it should not be used as production payment code. Having built PCI-scoped systems, I can tell you the hard parts are in exactly the places a demo skips: data handling, network segmentation, auditing, and key management. What the demo does capture is the *orchestration* problem that sits on top of all that.

## Try it yourself

1. Clone [the repo](https://github.com/jbirdkerr/temporal-order-processing).
2. Run `docker compose --profile app up --build`.
3. Open the Temporal UI at `http://localhost:8233`.
4. Submit an order with a `POST` to `http://localhost:8080/orders`, then check it with `GET /orders/ORD-1001`.
5. Trigger the failure scenarios documented in [FILL IN: path to docs in the repo].
6. Run the tests with `go test -v -race -count=1 ./...`.

If you break it in an interesting way, I'd like to hear about it. [COMMENTS / CONTACT LINK]
