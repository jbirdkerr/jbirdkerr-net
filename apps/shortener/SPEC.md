# URL Shortener with Analytics: Project Spec

## Summary

A small URL shortener served from `go.jbirdkerr.net`, built with FastAPI and deployed as a Python Worker on Cloudflare. It creates short links, redirects visitors, and records privacy-conscious aggregate click statistics in Supabase. It is a featured project on jbirdkerr.net and should be simple enough to finish, useful enough to keep running, and clear enough to write about honestly.

## Goals

- Create short links for arbitrary HTTP(S) URLs through an authenticated API.
- Redirect visitors quickly and reliably.
- Expose aggregate analytics per link: total clicks, recent clicks, daily time series, top referrer hosts, and coarse country.
- Collect no personally identifying data beyond what the request needs to function.
- Stay within Cloudflare Workers free-tier limits and Supabase free-tier limits.

## Non-goals

- A public, anonymous link-creation form.
- User accounts, a web UI, and Supabase Auth sign-in in v1. These are Phase 2 (see below); v1 is the API only.
- Per-visitor tracking, cookies, fingerprinting, or storing IP addresses or full user agents.
- Custom analytics dashboards. Stats are exposed through the API only.
- Link expiry, QR codes, or link previews in v1 (possible later).

## Architecture

- **Runtime:** FastAPI application running on Cloudflare Workers (Python). FastAPI support on Python Workers should be confirmed against current Cloudflare docs before implementation.
- **Database:** Supabase Postgres, in a dedicated `shortener` schema, separate from the existing `site_metrics` and related tables.
- **Writes:** Each click calls a single RPC (`shortener.record_click`) that upserts into the daily counts table. Writes run in the background so the redirect response is not blocked on the database.
- **Domain:** `go.jbirdkerr.net`, a DNS record pointing at the Worker.

## Endpoints

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `GET` | `/{slug}` | none (rate-limited) | Redirect to the target URL and record a click. Return 404 for unknown or disabled slugs. |
| `POST` | `/api/links` | API key | Create a link. Body: `target` URL, optional custom `slug`. Returns slug and short URL. |
| `GET` | `/api/links/{slug}` | API key | Return link metadata (target, created date, disabled flag). |
| `GET` | `/api/links/{slug}/stats?range=7d` | API key | Return aggregates for `range` (for example `24h`, `7d`, `30d`). |
| `DELETE` | `/api/links/{slug}` | API key | Disable a link. Soft-delete, so the slug is not reused. |

Redirects should use `302` by default so link changes take effect immediately and browsers do not cache the redirect.

## Link creation rules

- Target must be an absolute `http` or `https` URL. Reject other schemes, including `javascript:`, `data:`, and `file:`.
- Reject targets that point back to `go.jbirdkerr.net` to avoid redirect loops.
- Check targets against a blocklist. Start with a simple in-config list and keep the check easy to swap for a provider later.
- Slugs: if none is given, generate a random short slug of about 6 to 8 characters from a URL-safe alphabet. Custom slugs must match a strict pattern (letters, digits, hyphens) and must not collide with reserved paths such as `api`, `health`, and `static`.
- Creating the same target twice may return the existing slug or create a new one. Pick one behavior and document it.

## Authentication

- Creation, metadata, stats, and deletion require an API key sent as a bearer token.
- Keys are issued out-of-band. Only a hash of each key is stored in the database. The raw key is shown once at issuance.
- Store the key table in the `shortener` schema, with a `owner` or `label` column for identifying keys.

## Rate limiting

- **Creation:** per-API-key limit, for example a token bucket stored in Postgres or enforced through a Cloudflare rate-limiting rule. The exact limit is a configuration value.
- **Redirects:** per-IP limit on `GET /{slug}` to prevent scraping and abuse. Prefer a Cloudflare rate-limiting rule where possible so the Worker does not spend CPU on it.
- Rate-limit responses return `429` with a `Retry-After` header.

## Data model

```sql
create schema if not exists shortener;

create table shortener.links (
  slug text primary key,
  target text not null,
  owner_id uuid not null,
  created_at timestamptz not null default now(),
  disabled boolean not null default false
);

create table shortener.api_keys (
  key_hash text primary key,
  label text not null,
  created_at timestamptz not null default now(),
  revoked boolean not null default false
);

create table shortener.click_daily (
  slug text not null references shortener.links(slug),
  day date not null,
  country text not null default 'unknown',
  referrer_host text not null default 'direct',
  clicks integer not null default 0,
  primary key (slug, day, country, referrer_host)
);
```

Notes:

- `owner_id` references the Supabase `auth.users` ID of the user who created the link. Links created with an API key take the owner of that key. In Phase 1, before accounts exist, `owner_id` can hold the key owner's ID from `shortener.api_keys`, and it is migrated to real user IDs in Phase 2.
- Keep `click_daily` aggregate-only. No IPs, user agents, or full referrer URLs.
- `country` comes from Cloudflare request metadata where available, otherwise `unknown`.
- `referrer_host` is the hostname of the `Referer` header only, with `direct` when absent.

## The `record_click` RPC

- Input: slug, current UTC day, country, referrer host.
- Behavior: upsert into `shortener.click_daily`, incrementing `clicks` by 1.
- Should be a Postgres function, deployed through a migration, with grants set deliberately for the service role.
- Multi-row writes, if any, should also go through an RPC to stay atomic.

## Stats response

`GET /api/links/{slug}/stats` should return JSON with:

- `slug`, `target`, `created_at`, `disabled`
- `total_clicks` (all time)
- `range` and `clicks_in_range`
- `daily`: array of `{ day, clicks }` for the requested range, with zero-filled days
- `top_referrers`: array of `{ host, clicks }`, limited to the top N
- `top_countries`: array of `{ country, clicks }`, limited to the top N

## Privacy requirements

- Do not store IP addresses, full user agents, cookies, or any per-visitor identifier.
- Do not add third-party trackers or scripts to the redirect path.
- Document in the README what is collected and what is deliberately not collected. This is part of the project's story.

## Constraints to design around

- **Cloudflare Workers Free:** 100,000 requests per day and 1,000 per minute, both account-wide and shared with other Workers on the same account (including the existing metrics worker). Reset is at midnight UTC.
- **CPU time:** 10 ms per request on the free plan. Measure real CPU time with FastAPI loaded, since import and framework overhead may matter.
- **Subrequests:** 50 per invocation. Each click's database write counts toward this, so avoid extra calls per request.
- **Variables:** 64 secrets and text values per Worker.
- **Supabase Free:** verify current limits on database size, request volume, and inactivity pausing before relying on it for public redirects.

## Deployment and configuration

- Worker config in `wrangler.toml`. Non-secret values (for example the Supabase URL) as plain vars.
- Secrets (for example the Supabase service-role key) set only through Wrangler, never committed.
- Migrations live in the repo and are applied to Supabase deliberately, not automatically.
- Do not deploy or run migrations against production without explicit approval.

## Delivery phases

Build in this order. The public redirect path stays unauthenticated and simple throughout.

1. **Phase 1: API core.** Link creation, redirects, click recording, stats, and API-key auth, as specified above.
2. **Phase 2: Web interface and Supabase Auth.** Described below.

## Phase 2: Web interface and Supabase Auth

### Goals

- Let a signed-in user create, view, and disable their own links in a browser.
- Show per-link stats in the browser.
- Use Supabase Auth for sign-in, with API keys still available for scripts.

### Stack

- Pages are rendered by FastAPI as HTML fragments and full pages. No JavaScript build step and no frontend framework.
- HTMX drives partial updates: the link list, the create form, and the stats panel.
- Styling stays minimal and consistent with the terminal look of jbirdkerr.net where practical.

### Routes

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `GET` | `/` | optional | Landing page; shows the dashboard if signed in, sign-in prompt otherwise. |
| `GET` | `/login` | none | Sign-in page with magic-link and OAuth options. |
| `GET` | `/auth/callback` | none | Receives the Supabase auth redirect, exchanges it for a session, sets the session cookie, and redirects to `/`. |
| `POST` | `/logout` | session | Clears the session cookie. |
| `GET` | `/links` | session | HTML fragment: the signed-in user's links. |
| `POST` | `/links` | session + CSRF | Create a link from the form; returns the updated row as a fragment. |
| `GET` | `/links/{slug}/stats` | session, owner only | HTML fragment: stats panel for one link. |
| `POST` | `/links/{slug}/disable` | session + CSRF, owner only | Disable a link; returns the updated row. |

Redirects, `GET /{slug}`, stay public. The web routes must not shadow reserved paths such as `api`, `health`, `static`, `login`, `logout`, and `auth`. Add these to the reserved slug list.

### Supabase Auth

- Sign-in methods: email magic link, and OAuth with GitHub. Add others only if needed.
- Supabase issues the access token. The `/auth/callback` route exchanges it server-side and sets a session cookie.
- The Worker verifies the JWT on each request before trusting the user ID. Verification uses the project's JWT signing configuration; confirm the current verification approach against Supabase docs before implementing.

### Session handling

- The session cookie is `httpOnly`, `Secure`, `SameSite=Lax`, and scoped to `go.jbirdkerr.net`. The Supabase access token is never stored in `localStorage` or readable by page JavaScript.
- Set a sensible session lifetime, and refresh or re-authenticate when it expires. The refresh flow should be confirmed against Supabase's current documentation.
- Sign-out clears the cookie. It does not need to revoke sessions server-side in v1, but this trade-off should be noted in the README.

### CSRF

- All state-changing requests (`POST`) require a CSRF token. The token is set in the page's `<body>` through `hx-headers` so HTMX sends it automatically, and the server checks it on each request.
- Combined with `SameSite=Lax`, this covers forms posted from other sites.

### Authorization

- Users see and change only links where `owner_id` matches their own user ID.
- Owner checks run on the server for every stats and disable request. Hiding buttons in the UI is not enough.
- Requests for another user's slug return `404`, not `403`, so slug existence is not leaked.

### Data changes

- `owner_id` becomes a Supabase `auth.users` reference. Migrate Phase 1 owners to real user IDs, or document that Phase 1 links remain owned by the original key owner.
- Store only what sign-in requires. Do not store the email address in `shortener` tables beyond what Supabase Auth already holds. Display names are optional and should not be added unless needed.
- Each change ships as a new timestamped migration.

### Privacy

- The README must state what the sign-in collects (email or GitHub identity, as provided by the chosen method) and what it does not collect.
- Stats remain aggregate-only, as in Phase 1. Signed-in users do not gain access to per-visitor data.

### Phase 2 constraints

- Each page and HTMX fragment request counts toward the same Cloudflare daily and per-minute budget as redirects. Keep the dashboard lightweight and avoid polling.
- Supabase Auth's current user and request limits on the free tier should be verified before opening sign-up to others. Consider an invite-only or allowlist mode for early use.

### Phase 2 acceptance criteria

- A new user can sign in with a magic link or GitHub and land on the dashboard.
- A signed-in user can create a link from the form and see it appear without a full page reload.
- A user cannot view or disable another user's link, including by guessing slugs. Those requests return `404`.
- POST requests without a valid CSRF token are rejected.
- The session cookie is `httpOnly`, `Secure`, and `SameSite=Lax`, and no Supabase token is readable from page JavaScript.
- Sign-out clears the session.
- Public redirects work with no session and no change in behavior.
- The README documents sign-in, what is collected, and the session lifetime.

## Open questions

- Repository: a separate repo, or a folder in the existing `jbirdkerr-net` repo? The existing repo's AGENTS.md applies to that repo, so a separate repo may be cleaner.
- Supabase: confirm the free-tier limits and whether the shortener should share the existing project or use its own. Also confirm Supabase Auth's limits and the current JWT verification and session refresh approach.
- Phase 1 owner migration: migrate existing links to real user IDs in Phase 2, or leave them with the original key owner?
- Duplicate targets: reuse the existing slug or create a new one?
- Country data: confirm the Cloudflare header available on the Worker runtime.
- Rate-limit values: decide the creation and redirect thresholds.
- Invite-only or open sign-up for Phase 2?
- Duplicate targets: reuse the existing slug or create a new one?
- Country data: confirm the Cloudflare header available on the Worker runtime.
- Rate-limit values: decide the creation and redirect thresholds.

## Acceptance criteria

- `POST /api/links` with a valid key and URL returns a working short URL.
- `GET /{slug}` returns `302` to the target and increments the day's click count in Supabase without delaying the response on the database write.
- Invalid targets, unsafe schemes, and loop targets are rejected with clear `4xx` errors.
- Requests without a valid key to protected endpoints return `401`.
- Stats for a range return zero-filled daily counts and correct totals.
- Rate limits return `429` with `Retry-After`.
- No IP addresses, user agents, or cookies are written to the database. This is verified by inspecting the schema and the write path.
- Migrations are timestamped and do not edit applied migrations.
- The README documents the endpoints, the data collected, and the setup steps.
