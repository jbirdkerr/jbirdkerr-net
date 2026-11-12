# AppOps — Plan for Phase 6 (metrics-worker schema move) and Phase 7 (documentation)

Status: **Phase 6 files are written and tested on a scratch database; nothing has been applied to the live database, and nothing is merged to `main`.** Decisions taken: accept the short cutover window (option A), and rehearse in a rolled-back transaction before any real apply.
Phase 5 (multiple apps) stays in `IMPLEMENTATION.md`; its mock-based tests pass, and live verification with a real second app is deferred.

## Phase 6 — Move metrics-worker from `public` to `metrics_worker`

### What the repository inspection found

| Area | Finding | Consequence |
|---|---|---|
| Objects | Tables `site_events`, `site_event_counts`, `site_metrics`, `worker_health_stats`; functions `record_site_events(jsonb)`, `record_worker_health(...)`; the identity sequence behind `site_events.id` | All must move. Owned sequences move with `ALTER TABLE ... SET SCHEMA`; functions do not get rewritten by it. |
| Function bodies | Both are `SECURITY DEFINER` with `SET search_path TO 'public'` and `public.`-qualified references in the body | `ALTER FUNCTION ... SET SCHEMA` alone breaks them at runtime. They must be recreated with `metrics_worker` references. |
| Worker access | `worker.py` uses the PostgREST API with the service-role key: `/rest/v1/rpc/record_site_events`, `/rest/v1/rpc/record_worker_health`, and reads of `site_metrics`, `site_event_counts`, `worker_health_stats` (about five call sites plus `supabase_request`) | PostgREST serves only exposed schemas and picks one via the `Accept-Profile` (reads) or `Content-Profile` (writes and RPC) header. Without them the worker keeps targeting `public`. |
| API exposure | `db/supabase/config.toml` has `[api] schemas = ["public", "graphql_public"]` | `metrics_worker` must be added locally and exposed on the hosted project (dashboard API settings, or `supabase config push`). |
| Seed data | `db/supabase/seed.sql` inserts into `"public"."site_*"` and `"public"."worker_health_stats"` and calls `setval('"public"."site_events_id_seq"')` (5 places) | After the migration, `supabase db reset` would fail until these are changed to `metrics_worker`. |
| RLS and policies | Policies and `ENABLE ROW LEVEL SECURITY` live on the tables | They move with the tables. `anon`, `authenticated` and `service_role` additionally need `USAGE` on the new schema. |
| Realtime | `site_metrics` is in the `supabase_realtime` publication. No client in this repo subscribes (checked `blog/src`, `apps/boopmaster`) | Publication membership follows the table through a schema move. Low risk, but confirm with a query after the move. |
| Schema owner | AppOps already created `metrics_worker` (owner `postgres`) on the live database | The migration must use `CREATE SCHEMA IF NOT EXISTS` so it also works on a fresh local database. |
| Role grants | `ALTER TABLE ... SET SCHEMA` keeps existing ACLs and does not apply default privileges | `app_metrics_worker_dev` will not automatically get privileges on the moved tables. See "AppOps follow-up". |
| Test coverage | `apps/metrics-worker/tests/test_worker.py` covers decoding and rendering only, with no HTTP header or URL assertions | The header change needs a new small unit test. |
| Rules | `AGENTS.md`: new timestamped migration, never rewrite an applied one, and no production migration unless explicitly requested. Pushes to `main` run `supabase db push` and then deploy the worker | Merging this work to `main` is what applies it to production. |

### Proposed changes (all files; nothing applied)

1. **New migration** `db/supabase/migrations/<timestamp>_move-metrics-worker-schema.sql`:
   - `CREATE SCHEMA IF NOT EXISTS metrics_worker;`
   - `GRANT USAGE ON SCHEMA metrics_worker TO anon, authenticated, service_role;`
   - `ALTER TABLE public.<each of the four> SET SCHEMA metrics_worker;`
   - `ALTER FUNCTION public.record_site_events(jsonb) SET SCHEMA metrics_worker;` and the same for `record_worker_health(...)` (keeps the existing `EXECUTE` grants), then `CREATE OR REPLACE FUNCTION metrics_worker.<name>` with `SET search_path TO 'metrics_worker'` and every `public.` reference in the body changed to `metrics_worker.`.
2. **`apps/metrics-worker/worker.py`**: define one schema constant and send `Content-Profile` on the two RPC POSTs and `Accept-Profile` in `supabase_request`. Add unit tests for the header helper.
3. **`db/supabase/seed.sql`**: change the 5 `public` references to `metrics_worker`.
4. **`db/supabase/config.toml`**: add `metrics_worker` to `[api] schemas`.
5. **Docs**: `apps/metrics-worker/docs/architecture.md` (and others that name `public.*`) updated in Phase 7.
6. `db/supabase/schemas/01-initial-schema.sql` is not used (`schema_paths = []`) and is left alone, noted as historical.

### Rehearsal (no Docker here, so use a rolled-back transaction)

Postgres DDL is transactional. Run the migration SQL against the live database inside `BEGIN; ... ROLLBACK;` as the admin user and, before the rollback:

- confirm the four tables, both functions and the sequence are in `metrics_worker` and `public` has none of them;
- `select metrics_worker.record_worker_health('health', 200);` and a `record_site_events('[{"event":"probe"}]')` call, then check the counters changed (these writes roll back);
- confirm `site_metrics` is still in `pg_publication_tables` for `supabase_realtime`;
- `set role service_role;` and select from a moved table to confirm schema `USAGE`.

Nothing persists. This tests the SQL against real data, which a local reset would not.

### Cutover and the compatibility window

CI order on merge to `main`: migration first (`db` unit), then the worker deploy (`metrics-worker` depends on `db`). Between the two, the old worker calls `public.*` objects that no longer exist, so persistence and the dashboard return errors for a few minutes. The worker already counts these (`total_supabase_write_failures`) and PostHog forwarding is unaffected, but some events are lost and the stats page errors.

Options:
- **A (recommended for this project): accept the short window.** Simplest. Lost events are a minute or two of site analytics.
- **B: compatibility shim.** A first migration adds `public` wrapper functions and `security_invoker` views over the moved tables, the worker deploys, and a later migration drops the shims. Zero errors, but more SQL and one more migration.

### Rollback

Keep the reverse SQL in the plan and rehearse it too: `ALTER TABLE ... SET SCHEMA public`, recreate the functions with `public` references, and redeploy the previous worker. Before applying, take a data backup of the four tables (`pg_dump --data-only -t ...` or the Supabase dashboard backup). The move itself is metadata only and does not copy rows.

### AppOps follow-up after the move

`app_metrics_worker_dev` has no privileges on the moved tables (default privileges only cover tables created later). The worker does not use this role yet (it stays on the REST API with `service_role`), so this does not block the cutover, but the isolation demo needs it. Options: `./run.sh refresh` then `up` (the provider should see the grant drift; **untested**), or a one-off `GRANT` run as admin. Test the Pulumi route first; fall back to SQL.

### Implemented (uncommitted to `main`; on the feature branch only)

- `db/supabase/migrations/20261111180000_move-metrics-worker-schema.sql` (function bodies copied from the original migrations with `public` replaced).
- `apps/metrics-worker/worker.py`: `DB_SCHEMA` constant and `supabase_headers()` (`Accept-Profile` / `Content-Profile`), with two new unit tests.
- `db/supabase/seed.sql` (5 references) and `db/supabase/config.toml` (`[api] schemas`).
- `infra/appops/docs/phase6/rehearsal.sql` and `rollback.sql`.

Checks actually run: pre-commit (including the worker tests) passes. On a **scratch Postgres 16 with stand-in `anon`/`authenticated`/`service_role` roles** (not Supabase, and not Postgres 17; the `MAINTAIN` privilege was stripped from the setup copy only), the rehearsal reported OK for all structural checks, both RPCs worked as `service_role` and incremented the counters, `anon` could read, and the transaction rolled back. A real apply followed by `rollback.sql` restored all objects to `public` with the function ACLs intact.

Not verified: behavior on the real Supabase instance (PostgREST schema exposure, Realtime, existing production grants), and `supabase db reset` with the changed seed (no Docker here).

### Steps for the live run

1. Rehearse (changes nothing): `psql "$ADMIN_DATABASE_URL" -v ON_ERROR_STOP=1 -f infra/appops/docs/phase6/rehearsal.sql` from the repository root. Every NOTICE should say OK, and both comparison columns should be `t`.
2. Back up the four tables before the real cutover.
3. Expose `metrics_worker` on the hosted project (dashboard: Settings → API → Exposed schemas, or `supabase config push`). Do this **before** merging, or the new worker gets errors from PostgREST.
4. Merge to `main`: the `db` unit applies the migration, then the worker deploys. Expect a few minutes of failed writes and stats errors.
5. Verify the worker (`/metrics`, health stats, event persistence). If it fails, run `phase6/rollback.sql` and redeploy the previous worker.
6. Restore app-role privileges on the moved tables (see "AppOps follow-up").

### Open decisions

1. The worker stays on the REST API with `service_role`; switching it to the AppOps credential is deferred.

## Phase 7 — Documentation

The README currently has overview, layout, rotation, removal and reserved schemas. Remaining items from `IMPLEMENTATION.md`, with where each goes and its state:

| Topic | Home | State |
|---|---|---|
| How to add an application | `README.md` (new "Adding an application" section) | Known and exercised for metrics-worker |
| Configure database permissions | `README.md` | Known |
| Run preview and deployment | `README.md`; `run.sh`, `.env` variables, required stack config (`secretsStoreId`, `appDb*`) | Known |
| Retrieve application credentials | `README.md` + wrangler `[[secrets_store_secrets]]` example from `RECONNAISSANCE.md` §3a | Binding example **untested** (worker is not consuming it) |
| Rotate credentials | Done (README) | Verified |
| Migrate an app to a different schema | New section | Changing `database.schema` replaces a `Protect`ed schema and fails by design. The procedure needs to be written from the metrics-worker migration and is untested beyond that. |
| Recover from partial provisioning failure | New section | Seen once (Cloudflare 403 mid-apply; re-run `up` resumed). Document that and the rotation partial-failure case. |
| Safely remove an application | Done (README) | **Untested** against a live database |
| Bootstrap and access the state backend | Link to `STATE_BACKEND_BOOTSTRAP.md` | Exists |
| Restore state from backup | New `STATE_BACKUP_RESTORE.md` | The spec requires an actual restore test. **Not done.** |
| Known limitations | README | Cloudflare secret not write-only in state; Realtime/REST path unchanged; single environment verified; grant drift after a schema move untested |

Also update the `metrics-worker` docs to the new schema, and keep `RECONNAISSANCE.md` milestone statuses current.

## Demo (tomorrow) — what can be shown safely

Verified and safe to run live: `appops.yaml` and the discovery rules; `./run.sh preview` showing no changes; the catalog queries for the role and its privileges; the probe-table isolation test; the Secrets Store entry; encrypted state in `stack export`; a rotation (`version` 3, then idempotent preview).

Do not show live without rehearsing first: removing an app (protected-delete behavior is untested), the Phase 6 cutover, and a state restore.
