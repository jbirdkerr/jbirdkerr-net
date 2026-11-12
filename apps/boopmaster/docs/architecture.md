# Boopmaster architecture (client side)

This page covers the browser app. For the worker, database and ingestion pipeline, see [`apps/metrics-worker/docs/architecture.md`](../../metrics-worker/docs/architecture.md) and [`metrics-pipeline.md`](../../metrics-worker/docs/metrics-pipeline.md).

## Overview

```text
Browser (Cloudflare Pages: boopmaster3000.jbirdkerr.net)
  index.html + css/style.css + js/main.js
    ├─ PostHog JS SDK ──events──▶ metrics-api.jbirdkerr.net (Cloudflare Worker)
    │                               ├─▶ PostHog Cloud
    │                               └─▶ Supabase (metrics_worker schema)
    └─ HTMX stats panel ◀─HTML─── metrics-api.jbirdkerr.net /metrics, /health-metrics
```

The static site contains no secrets and no server code. All data access happens in the worker.

## `js/main.js` sections

| Section | What it does |
| --- | --- |
| PostHog init | Loads the SDK with `api_host` set to the worker proxy, so ad blockers that block PostHog domains do not break capture. `captureEvent()` wraps `posthog.capture`. |
| Modal system | The stats panel (`M`) and credits panel. Opening the stats panel loads `/metrics` and `/health-metrics` with `htmx.ajax` and starts 5-second polling per tab. Polling stops when the panel closes, the tab is hidden, or the other tab is active. |
| WebAudio | Boop, bark and treat sounds are synthesized at runtime from oscillators. The `AudioContext` is created on first use, after a gesture. |
| Boop | Snoot hotspot or `B`. Random pitch, squish animation, a floating phrase badge, and a running `combo_count`. |
| Eye tracking | Iris offsets follow the pointer or touch. Travelled distance is accumulated and sent as `eye_movement` events, flushed about every 3 seconds once at least 10 px moved, and on `beforeunload`/`pagehide`. |
| Googly eyes, party mode, treats | Toggles and a particle shower; party mode adds a body class and banner. |
| Keyboard and console API | Shortcuts in the README; helpers on `window` and `window.HenryBeard`. Shortcuts are ignored while focus is in an input or textarea. |

## Stats panel

The panel is server-rendered HTML from the worker, swapped into `#metrics-container` and `#health-container`. The browser only polls while the panel is open and visible. The worker applies a short edge cache to `/metrics`, so values can lag a few seconds behind the database.

## Hosting

Cloudflare Pages project `boopmaster3000` (`wrangler.toml`). The Pages project serves the whole directory, so Markdown and `docs/` are public. The main workflow deploys it when the `boopmaster` unit in `deploy/units.toml` is affected. See the [README](../README.md#deploy).
