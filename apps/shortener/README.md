# go.jbirdkerr.net: URL shortener

A small URL shortener built with FastAPI and deployed as a Python Worker on Cloudflare. It creates short
links, redirects visitors, and records aggregate click statistics in Supabase.

This is **Phase 1** (API only). The web interface and Supabase Auth are Phase 2; see `SPEC.md`.

## Endpoints

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `GET` | `/{slug}` | none | `302` to the target and record a click. `404` for unknown or disabled slugs. |
| `POST` | `/api/links` | API key | Create a link. Body: `{"target": "https://...", "slug": "optional-custom"}`. |
| `GET` | `/api/links/{slug}` | API key | Link metadata (target, created date, disabled flag). |
| `GET` | `/api/links/{slug}/stats?range=7d` | API key | Aggregates for the range. |
| `DELETE` | `/api/links/{slug}` | API key | Disable a link (soft delete; the slug is never reused). |
| `GET` | `/health` | none | Liveness check. Makes no database call. |

Send the key as `Authorization: Bearer <key>`. Errors look like `{"error": {"code": "...", "message": "..."}}`.

```bash
curl -X POST https://go.jbirdkerr.net/api/links \
  -H "Authorization: Bearer $KEY" -H "Content-Type: application/json" \
  -d '{"target": "https://example.com/a/long/path"}'
# {"slug":"aB3xK9z","short_url":"https://go.jbirdkerr.net/aB3xK9z", ...}
```

### Behavior worth knowing

- **Duplicate targets:** creating the same target twice always creates a new slug, so each link keeps its own stats.
- **Ownership:** each API key has an `owner_id`. A key can only read, see stats for, or disable links that its
  owner created; other owners' slugs return `404`, the same as a missing slug.
- **Slugs:** generated slugs are 7 URL-safe letters/digits. Custom slugs are 3-32 characters (letters, digits,
  hyphens; starting and ending with a letter or digit). `Abc` and `abc` count as the same slug. Reserved:
  `api`, `health`, `static`, `login`, `logout`, `auth`, `links`, `docs`, `admin`, `favicon`, `robots`, `index`.
- **Target rules:** absolute `http`/`https` only; no credentials in the URL; no whitespace or backslashes;
  max 2048 characters; not this service's own host; not on the blocklist (`BLOCKED_HOSTS`, which also blocks
  subdomains). The check lives behind a small `Blocklist` interface (`links.py`) so a provider can replace it.
- **Stats ranges** are whole UTC days ending today, because clicks are stored per day. `7d` is today and the
  six days before it. Hour ranges round up to days (`24h` is today only). The response includes `from` and
  `to`. Maximum is 90 days (`MAX_RANGE_DAYS`).
- **Redirects** use `302` with `Cache-Control: no-store`. `HEAD` requests redirect but are not counted. Bots that
  follow links are counted like anyone else.

## What is collected

Per click, exactly four values are written, as a daily aggregate in `shortener.click_daily`:

| Value | Source |
| --- | --- |
| slug | the path |
| UTC day | the clock |
| country | Cloudflare's `CF-IPCountry` header, otherwise `unknown` |
| referrer host | the hostname of the `Referer` header only, otherwise `direct` |

The row stores a click count for that combination, nothing else.

## What is deliberately not collected

IP addresses, user agents, cookies, per-visitor identifiers, fingerprints, full referrer URLs (paths and query
strings are dropped before anything is sent), and anything from third-party trackers (there are none on the
redirect path). Because there is no per-visitor data, signed-in users in Phase 2 will not gain access to any.

The API-key table stores a SHA-256 hash of each key plus a label. Rate limiting stores a hit count per key and
time window. Neither involves visitors.

`tests/test_supabase_store.py` checks the write path (the click RPC receives exactly the four fields above) and
scans the migrations for visitor-identifying column names.

## Setup

Requires [uv](https://docs.astral.sh/uv/), Node.js (for Wrangler), and a Supabase project.

```bash
uv sync --extra test
uv run --extra test pytest          # 87 tests, no network or Supabase needed
```

### 1. Database

Migrations are plain SQL in `migrations/`, applied by you, deliberately. They are timestamped, and an applied
migration is never edited; changes ship as a new file.

1. Run `migrations/20261028120000_create_shortener_schema.sql`, then `..._shortener_rpcs.sql` (SQL editor or `psql`).
2. In Supabase: **Settings → API → Exposed schemas**, add `shortener`. PostgREST will not serve the schema
   until you do. Without it every call fails with an error about the schema not being found.

### 2. API keys

```bash
uv run python scripts/issue_key.py "my laptop"
```

Prints the raw key once and an `INSERT` that stores only its hash. Run the `INSERT` against Supabase. The
returned `owner_id` identifies everything that key creates.

### 3. Worker configuration

Non-secret values are in `wrangler.toml` (`SUPABASE_URL`, `BASE_URL`, `BLOCKED_HOSTS`, rate-limit values).
The service-role key is a secret and is never committed:

```bash
npx wrangler secret put SUPABASE_SERVICE_KEY
```

For local development copy `.dev.vars.example` to `.dev.vars`, then `uv run pywrangler dev`.

### 4. Rate limits

- **Creation** is limited per API key in Postgres (`shortener.check_rate_limit`, a fixed window). Defaults:
  30 per hour. Change `CREATE_RATE_LIMIT` and `CREATE_RATE_WINDOW_SECONDS`. Over the limit returns `429` with
  `Retry-After`.
- **Redirects** are limited per IP by a Cloudflare rate-limiting rule, not by the Worker, so the Worker does not
  spend CPU on it. Create the rule in the Cloudflare dashboard (Security → WAF → Rate limiting rules) for
  hostname `go.jbirdkerr.net`, excluding paths starting with `/api/`, at 60 requests per minute per IP,
  action: block (Cloudflare returns `429` with `Retry-After`). Check which limits and periods your plan allows.

## Limits to keep in mind

Cloudflare Workers Free allows 100,000 requests/day and 1,000/minute account-wide (shared with the metrics
worker), 10 ms CPU per request, and 50 subrequests per invocation. A redirect makes one database call before
responding (the lookup) and one after (the click write, in the background). Creation makes three (key lookup,
rate limit, insert). Check the current Supabase free-tier limits, especially inactivity pausing, before relying
on it for public redirects.

## Layout and Pants

```
src/worker.py          Worker entrypoint (asgi.entrypoint)
src/shortener/         app, auth, db (PostgREST client), links/stats (pure logic), config
migrations/            SQL, applied manually
scripts/issue_key.py   key issuing
tests/                 pytest; the database is faked in tests/conftest.py
BUILD                  Pants targets (requirements, sources, tests, migrations)
```

For Pants, `apps/shortener/src` needs to be a source root (`[source].root_patterns` in `pants.toml`).
Imports of `workers` and `pyodide` only exist inside the Cloudflare runtime, so those lines are marked
`# pants: no-infer-dep`. Run `pants test apps/shortener:tests`.
