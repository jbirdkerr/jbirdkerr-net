# 🛰️ Worker API Reference

The **metrics-api.jbirdkerr.net** edge service runs as a Python Cloudflare Worker (`metrics-worker/worker.py`). It acts as a reverse proxy for PostHog telemetry and a backend aggregator for Supabase metrics.

---

## Endpoints

### 1. `GET /metrics`
Retrieves aggregated stats from Supabase (`site_metrics` and `site_event_counts`) and renders an HTML dashboard fragment tailored for HTMX swap.

- **Request Headers**:
  - `Origin`: Validated against allowed origins.
- **Response**:
  - Content-Type: `text/html; charset=utf-8`
  - Cache-Control: `public, max-age=2, s-maxage=3`
- **Behavior**:
  - Excludes internal PostHog telemetry (`$*` events).
  - Embeds ISO UTC timestamps in `data-utc` attributes for client-side localized display.

---

### 2. `POST /i/v0/e/` & `POST /batch`
Proxies client event submissions directly to PostHog Cloud (`us.i.posthog.com`) while initiating asynchronous background persistence to Supabase.

- **Request**:
  - PostHog payload (JSON, or URL-encoded / base64 / gzipped string).
- **Response**:
  - Status: 200 OK (proxied from PostHog).
- **Background Task (`ctx.waitUntil`)**:
  - Decodes and parses payload.
  - Normalizes events and passes them into Supabase via `POST /rest/v1/rpc/record_site_events`.

---

### 3. `GET /static/*` & `GET /array/*`
Proxies PostHog JavaScript SDK script requests to PostHog's asset CDN (`us-assets.i.posthog.com`).

- **Response**:
  - Status: 200 OK
  - Cache-Control: `public, max-age=86400` (24-hour edge cache)
  - CORS: `Access-Control-Allow-Origin: *`
- **Purpose**: Prevents ad blockers from blocking client script downloads and ensures reliable event capture.

---

### 4. `GET /health`
Liveness probe returning current UTC timestamp.

- **Response**:
  - Status: 200 OK
  - Body: `{"status": "ok", "timestamp": "2026-10-01T01:53:25.000000+00:00"}`
  - Cache-Control: `no-store`

---

### 5. `OPTIONS *`
Dynamic CORS preflight handler.

- **Behavior**:
  - Validates `Origin` against `ALLOWED_ORIGINS` (`https://jbirdkerr.net`, `https://www.jbirdkerr.net`, `https://henry.jbirdkerr.net`).
  - Echoes back `Access-Control-Request-Headers` dynamically to avoid preflight header mismatches with PostHog SDK.

---

## 🔐 Environment Variables & Secrets

### Cloudflare Worker Configuration (`wrangler.toml`)
- `POSTHOG_PROJECT_ID`: PostHog Project ID string.
- `SUPABASE_URL`: Target Supabase project REST URL (`https://<project-ref>.supabase.co`).

### Secrets (Configured via Wrangler CLI)
```bash
# Production Supabase Service Role Key (used for backend RPC and queries)
npx wrangler secret put SUPABASE_SERVICE_ROLE_KEY --env production
```

---

## Related Documentation

- [System Architecture](architecture.md)
- [Metrics & Sequence Flows](metrics-pipeline.md)
- [Database Schema](../supabase/migrations/20260923042823_01-initial-schema.sql)
