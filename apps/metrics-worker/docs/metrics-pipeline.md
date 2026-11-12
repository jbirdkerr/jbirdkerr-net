# 🔄 Metrics Pipeline & Sequence Flows

This document details the sequence flows for event capture, parallel persistence, and live dashboard rendering in **jbirdkerr.net**.

---

## 1. Event Capture & Dual Ingestion Pipeline

When a user interacts with the canvas (boops the snoot, rolls the eyes, engages party mode, or showers treats), events are captured and written to both PostHog and Supabase in parallel.

```mermaid
sequenceDiagram
    autonumber
    actor User as 👤 Visitor
    participant Browser as 🌐 Browser (js/main.js)
    participant Worker as ☁️ Cloudflare Worker
    participant PostHog as 📊 PostHog Cloud
    participant Supabase as ⚡ Supabase (PostgreSQL)

    User->>Browser: Boops Snoot / Triggers Action
    Note over Browser: Plays WebAudio SFX & Spawns Particles (0ms)
    Browser->>Browser: PostHog SDK buffers & batches payload
    Browser->>Worker: POST /i/v0/e/ (JSON or gzip/base64 payload)

    par Forward to PostHog
        Worker->>PostHog: POST https://us.i.posthog.com/i/v0/e/
        PostHog-->>Worker: 200 OK
    and Persist to Supabase (Background)
        Worker->>Worker: Decode & normalize payload (JSON/Gzip/Base64)
        Worker->>Supabase: POST /rest/v1/rpc/record_site_events
        Note over Supabase: Atomic transaction updates:<br/>1. metrics_worker.site_events (insert log)<br/>2. metrics_worker.site_event_counts (upsert count)<br/>3. metrics_worker.site_metrics (increment totals & combos)
        Supabase-->>Worker: 200 OK (Updated metrics JSON)
    end

    Worker-->>Browser: 200 OK
```

### Detailed Pipeline Stages

1. **Immediate Feedback (0ms Latency)**: Audio synthesis and particle effects trigger instantly on the client thread without waiting on network I/O.
2. **Batching & Compression**: PostHog's JS SDK buffers interactions and sends them via `POST /i/v0/e/` or `/batch`, encoded as raw JSON or gzipped base64 strings.
3. **Edge Forking**:
   - The Cloudflare Python Worker immediately proxies the untouched payload to PostHog Ingestion (`us.i.posthog.com`).
   - Concurrently, inside an async background task (`ctx.waitUntil`), the worker decodes the payload, extracts custom metrics (e.g. boops, eye roll distance in px, treat counts), and dispatches them to Supabase.
4. **Atomic Transaction in Supabase**:
   - `record_site_events(p_events)` runs as a single PostgreSQL transaction.
   - Inserts raw events into `metrics_worker.site_events`.
   - Upserts aggregated totals into `metrics_worker.site_event_counts`.
   - Updates global counters in `metrics_worker.site_metrics`.

---

## 2. Live Dashboard Rendering Flow

When the metrics modal is opened, HTMX queries the Cloudflare Worker which retrieves the latest metrics from Supabase:

```mermaid
sequenceDiagram
    autonumber
    actor User as 👤 Visitor
    participant Modal as 📊 Metrics Modal (HTMX)
    participant Cache as ⚡ Edge Cache
    participant Worker as ☁️ Worker (/metrics)
    participant DB as ⚡ Supabase Tables

    User->>Modal: Clicks "📊 Metrics" or presses [M]
    Note over Modal: Immediately flushes pending eye distance
    Modal->>Cache: GET https://metrics-api.jbirdkerr.net/metrics

    alt Cache Hit (within 3 seconds)
        Cache-->>Modal: Return cached HTML response
    else Cache Miss
        Cache->>Worker: Invoke handle_metrics()
        par Query Summary & Events
            Worker->>DB: GET /rest/v1/site_metrics?select=*
            DB-->>Worker: Summary row JSON
        and Query Event Breakdown
            Worker->>DB: GET /rest/v1/site_event_counts?order=count.desc&limit=30
            DB-->>Worker: Event counts JSON
        end
        Worker->>Worker: Filter out internal ($*) events & render HTML
        Worker-->>Cache: HTML response (Cache-Control: s-maxage=3)
        Cache-->>Modal: Swaps HTML into #metrics-container
    end

    Note over Modal: JavaScript converts UTC timestamp to visitor's local timezone
```

### Key Performance & Caching Features

- **Pending Queue Flush**: Clicking "📊 Metrics" triggers a flush of any in-flight eye movement distance before querying the stats, ensuring immediate accuracy.
- **Edge Cache TTL**: Cached for 3 seconds (`s-maxage=3`) on Cloudflare's edge network, shielding the database from heavy concurrent polling while keeping stats virtually real-time.
- **Server-Side HTML Rendering**: The worker compiles the HTML directly using semantic Tailwind/retro-styled markup and emits UTC ISO timestamps (`data-utc`).
- **Client-Side Local Timezone Translation**: A tiny inline script parses `data-utc` elements to display "Last updated" timestamps in the visitor's local time format.

---

## Related Documentation

- [System Architecture](architecture.md)
- [Worker API Reference](api.md)
- [Database Schema](../supabase/migrations/20260923042823_01-initial-schema.sql)
