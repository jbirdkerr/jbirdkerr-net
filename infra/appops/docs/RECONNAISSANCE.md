# AppOps — Initial Repository Reconnaissance Report

> [!NOTE]
> **Decisions captured from review:**
> 1. AppOps provisions credentials for all apps going forward. metrics-worker stays on the Supabase REST API for now; refactoring to a direct PG connection is deferred.
> 2. Cloudflare Secrets Store research is included below (§ 3a).
> 3. The correct application name is `metrics-worker` (plural). The implementation doc's references to `metrics-worker` are outdated.
> 4. The Pulumi state backend PostgreSQL server will be set up by the user before implementation begins. A bootstrap guide is provided as a companion document.

## 1. Repository Structure Relevant to AppOps

### Application directories under `apps/`

| Directory | Type | Uses PostgreSQL | Notes |
|---|---|---|---|
| `boopmaster/` | Static JS/WebAudio, Cloudflare Pages | **No** — pure static | No database connection; no `appops.yaml` candidate |
| `digdog/` | Go/Ebitengine → WebAssembly, Cloudflare Pages | **No** — pure static | Has its own `go.mod` (Ebitengine game), no DB usage |
| `metrics-worker/` | Python Cloudflare Worker | **Yes** — via Supabase REST API | Primary onboarding candidate |
| `url-shortener/` | Design spec only | **Planned** — spec calls for a `shortener` schema | No implementation yet; future candidate |

### Database and migrations

- **Shared migration directory:** `db/supabase/migrations/` — three timestamped SQL files, applied via `supabase db push` (the Supabase CLI).
- **Declarative schema file:** `db/supabase/schemas/01-initial-schema.sql` — same content as the first migration; the pgdelta engine is enabled but `schema_paths` is currently `[]` in the config, so migrations are the authoritative path.
- **Config:** `db/supabase/config.toml` — `major_version = 17`, pgdelta enabled, `schema_paths = []`, `db.migrations.enabled = true`.
- **Seed data:** `db/supabase/seed.sql` — large pg_dump restoring production data into the local dev environment.

### Current database objects (all in `public`)

| Object | Type | Created in Migration |
|---|---|---|
| `site_events` | Table | `01-initial-schema` |
| `site_event_counts` | Table | `01-initial-schema` |
| `site_metrics` | Table (singleton) | `01-initial-schema` |
| `worker_health_stats` | Table (singleton) | `02-worker-health-stats` |
| `record_site_events(jsonb)` | Function (`SECURITY DEFINER`) | `01-initial-schema` |
| `record_worker_health(...)` | Function (`SECURITY DEFINER`) | `02-worker-health-stats` |

All tables use `ENABLE ROW LEVEL SECURITY`. The functions have `SET search_path TO 'public'` and are explicitly `SECURITY DEFINER`, meaning they execute as their defining role regardless of the caller's `search_path`.

### Current roles and grants

The migrations grant to Supabase's built-in roles:
- `postgres` (the superuser / owner)
- `service_role` (the worker's runtime identity)
- `anon`, `authenticated` (browser-facing, read-only RLS policies)

There are no application-specific PostgreSQL roles today.

> [!IMPORTANT]
> **Object ownership:** All objects are owned by `postgres` (the Supabase admin user). The Supabase CLI runs `supabase db push` which connects as `postgres`. This is the migration identity — it both creates objects and owns them.

### How the metrics-worker connects

The worker does **not** use a direct PostgreSQL connection string. It calls the Supabase **REST API** (PostgREST) with a service-role JWT:

```python
# From worker.py
f"{env.SUPABASE_URL}/rest/v1/rpc/record_site_events"
headers = {
    "apikey": env.SUPABASE_SERVICE_ROLE_KEY,
    "Authorization": f"Bearer {env.SUPABASE_SERVICE_ROLE_KEY}",
}
```

Environment variables come from:
- `SUPABASE_URL` — set in `apps/metrics-worker/wrangler.toml` `[env.production.vars]`
- `SUPABASE_SERVICE_ROLE_KEY` — set as a Wrangler secret (`wrangler secret put`)

### Cloudflare integration

| Resource | How secrets are consumed |
|---|---|
| `CLOUDFLARE_API_TOKEN` | GitHub Actions secret → `wrangler deploy` |
| `CLOUDFLARE_ACCOUNT_ID` | GitHub Actions secret → `wrangler deploy` |
| `SUPABASE_SERVICE_ROLE_KEY` | Wrangler secret (`wrangler secret put`) |
| `SUPABASE_URL` | `wrangler.toml` plain-text var |

There is **no existing use of Cloudflare Secrets Store**. The metrics-worker uses Wrangler secrets (Workers environment secrets), which are a different mechanism. AppOps would introduce Secrets Store as a new integration pattern.

### Pulumi and Go

- **No existing Pulumi projects.** The `infra/appops/` directory contains only `IMPLEMENTATION.md` and `PREWORK.md`.
- **Existing Go module:** `apps/digdog/go.mod` — an Ebitengine game, unrelated to infrastructure.
- **No Go root module** at the repository root.
- The AppOps Pulumi project and `go.mod` will be entirely new.

### Deployment tooling

- `deploy/units.toml` — unit manifest for CI's affected-path detection.
- `.github/workflows/deploy.yml` — the main CI/CD pipeline. Supabase migrations are applied via `npx supabase db push`.
- Migrations run as the `postgres` role through the Supabase CLI. The CLI does not target individual schemas — it applies all files in `db/supabase/migrations/` in timestamp order.

---

## 2. Applications to Onboard Initially

| Application | Proposed schema | Rationale |
|---|---|---|
| **metrics-worker** | `metrics_worker` | Only current PostgreSQL user. Validates the full vertical slice. |

**Deferred candidates:**

| Application | Status | Why deferred |
|---|---|---|
| url-shortener | Spec only, no implementation | Its spec already plans a `shortener` schema; can be onboarded when it's built |
| boopmaster | No DB usage | Pure static app |
| digdog | No DB usage | Pure static app |
| blog | No DB usage via PostgreSQL | Astro static site deployed to GitHub Pages |

---

## 3. Discrepancies Between IMPLEMENTATION.md and the Actual Repository

### Example applications don't exist
- The implementation doc uses `blog` and `stats` as example application names. Neither uses PostgreSQL today. This is acknowledged in the document ("Do not assume the example directory names match the actual repository"), but worth noting — the only real onboarding candidate is metrics-worker.

### No direct PostgreSQL connection today
- The implementation document assumes applications use "ordinary PostgreSQL connection strings and credentials." The metrics-worker currently uses the **Supabase REST API** with a service-role JWT — not a direct `postgresql://` connection string.
- **Decision:** AppOps provisions credentials for all apps going forward. The metrics-worker remains on the REST API for now. Its `appops.yaml` will still be created so AppOps can provision its schema and role, but the worker won't consume the connection string until a future refactoring.

### Cloudflare Secrets Store is new
- The repo currently uses **Wrangler secrets** (Workers environment variables), not Cloudflare Secrets Store. Introducing Secrets Store is a new integration pattern.
- **Viability confirmed** — see § 3a below for full details on Secrets Store availability, Pulumi provider support, and Python Worker binding access.

### 3a. Cloudflare Secrets Store — Research Findings

#### What is Secrets Store?

Cloudflare Secrets Store is an **account-level** secrets management service. Unlike per-Worker Wrangler secrets, Secrets Store secrets:
- Live centrally and can be bound to one or more Workers.
- Can be rotated without redeploying Worker code.
- Are fetched asynchronously at runtime (not injected as plain environment strings).

#### Pulumi provider support

The Pulumi Cloudflare provider (v6) has native resources for Secrets Store:

| Resource | Go constructor | Purpose |
|---|---|---|
| `SecretsStoreSecret` | `cloudflare.NewSecretsStoreSecret(...)` | Create/update a secret in an existing store |

**`SecretsStoreSecretArgs` fields:**

```go
&cloudflare.SecretsStoreSecretArgs{
    AccountId: pulumi.String("..."),         // Cloudflare account ID
    StoreId:   pulumi.String("..."),         // ID of the Secrets Store
    Name:      pulumi.String("APP_METRICS_WORKER_DEV_DATABASE_URL"),
    Value:     generatedPassword.Result,     // write-only, stored encrypted in Pulumi state
    Scopes:    pulumi.StringArray{pulumi.String("workers")},
    Comment:   pulumi.String("Managed by AppOps"),
}
```

> [!IMPORTANT]
> The `Value` field is write-only. Pulumi cannot read it back from Cloudflare after creation. This means:
> - `pulumi up` with no config changes produces no diff (good — idempotent).
> - If someone manually changes the secret value in Cloudflare, Pulumi won't detect the drift.
> - Rotation must be driven through AppOps (bumping a version counter), not through the Cloudflare dashboard.

#### Store provisioning

The Secrets Store itself (the container) needs to exist before secrets can be created in it. Options:
1. **Create the store manually via Wrangler CLI** — `npx wrangler secrets-store store create appops --remote` — and configure the resulting `store_id` in Pulumi stack config.
2. **Create the store via Pulumi** — if a `NewSecretsStoreStore` resource exists in the provider version used. This should be verified during Milestone 3.

**Recommendation:** Create the store manually (it's a one-time operation) and put the `store_id` in Pulumi config. This keeps the bootstrap simple and avoids a chicken-and-egg dependency.

#### How a Python Worker consumes Secrets Store secrets

**Binding configuration** in `wrangler.toml`:

```toml
[[secrets_store_secrets]]
binding = "DATABASE_URL"
store_id = "<store-id>"
secret_name = "APP_METRICS_WORKER_DEV_DATABASE_URL"
```

**Python Worker access** (asynchronous `.get()`):

```python
class Default(WorkerEntrypoint):
    async def fetch(self, request):
        # Secrets Store bindings are NOT plain strings on env.
        # They require an async .get() call.
        db_url = await self.env.DATABASE_URL.get()
        # Use db_url...
```

This is a different access pattern from the current `env.SUPABASE_SERVICE_ROLE_KEY` (which is a plain string from Wrangler secrets). When/if the metrics-worker is refactored to use the AppOps-provisioned credential, the access code will need to use `await env.DATABASE_URL.get()`.

#### Local development

Secrets Store bindings are **not available locally** during `wrangler dev`. For local development, use `.dev.vars` to provide a local override, or configure a local Wrangler secrets store.

#### Plan impact

| AppOps Milestone | Secrets Store action |
|---|---|
| Pre-implementation | Create the Secrets Store via Wrangler CLI; record `store_id` |
| Milestone 1 | Add `store_id` to `Pulumi.dev.yaml` config |
| Milestone 3 | `NewSecretsStoreSecret` creates per-app secrets |
| Future (per app) | Add `[[secrets_store_secrets]]` binding to each app's `wrangler.toml` |

### Supabase `SECURITY DEFINER` functions and `search_path`
- Both `record_site_events` and `record_worker_health` use `SECURITY DEFINER` with `SET search_path TO 'public'`. Moving tables to a `metrics_worker` schema means these functions would need their `search_path` and unqualified table references updated, or the tables would need to remain qualified as `public.*` until the functions are rewritten to reference `metrics_worker.*`.

### Migration tooling cannot target individual schemas
- `supabase db push` applies all migrations in order. There is no native mechanism to scope a migration to a particular schema — each migration SQL file is responsible for qualifying its own object references. This is compatible with AppOps (AppOps creates schemas, migrations create objects within them), but migration SQL will need explicit schema prefixes.

---

## 4. Important Unknowns and Decisions

> [!WARNING]
> These need to be resolved before or during implementation.

### Must resolve before Phase 2 (Bootstrap)

1. **State backend PostgreSQL server** — The user will provision a dedicated PostgreSQL state server before implementation. See the companion [State Backend Bootstrap Guide](STATE_BACKEND_BOOTSTRAP.md) for what needs to be set up.

2. ~~**Cloudflare Secrets Store access**~~ — **Resolved.** Secrets Store is supported, the Pulumi provider has native resources, and Python Workers can consume secrets via async `.get()` bindings. See § 3a above.

3. **Direct PostgreSQL access to Supabase** — AppOps will provision PostgreSQL roles via the Pulumi PostgreSQL provider, which requires a direct `postgres://` connection to the Supabase instance (not the REST API). Confirm:
   - The direct connection host/port (the connection pooler URL in `.temp/pooler-url` suggests one exists).
   - Whether the `postgres` superuser password is available for the Pulumi provider to authenticate.
   - Any IP allowlisting or SSL requirements.

### Must resolve before Phase 4 (Provision one application)

4. **Migration identity for default privileges** — Objects are currently created by the `postgres` role. For AppOps default privileges to work, they must be configured for objects created by `postgres` in the target schema. Verify that `ALTER DEFAULT PRIVILEGES FOR ROLE postgres IN SCHEMA metrics_worker GRANT ...` is the correct approach.

5. **Supabase-managed roles and RLS** — Supabase creates and manages `anon`, `authenticated`, `service_role`, and `postgres` roles. AppOps-created roles (`app_metrics_worker_dev`, etc.) would be custom PostgreSQL roles. Confirm:
   - Whether custom roles can be created on Supabase-hosted PostgreSQL.
   - How RLS policies interact with custom roles (they currently target `anon`, `authenticated`).
   - Whether the new runtime role needs to bypass RLS or have RLS policies created for it.

### Must resolve before Phase 6 (metrics-worker migration)

6. **Application refactoring scope** — **Deferred.** The metrics-worker stays on the Supabase REST API for now. AppOps will provision its schema and role, but the worker won't consume the direct connection string until a future refactoring. This means the initial AppOps vertical slice validates provisioning and Secrets Store storage, but the end-to-end "app reads from Secrets Store" flow is deferred.

7. **Supabase Realtime dependency** — `site_metrics` is published to `supabase_realtime`. Moving it to a different schema may affect Realtime subscriptions. Verify compatibility.

8. **`SECURITY DEFINER` function migration** — Both RPC functions hardcode `SET search_path TO 'public'` and reference `public.` tables. A schema move requires rewriting these functions.

---

## 5. Proposed Implementation Sequence

### Milestone 1 — Pulumi project scaffold and discovery (Completed)

**Goal:** A Go module and Pulumi project that discovers `appops.yaml` configurations, validates identifiers, and shows planned resources without creating anything.

- [x] Create `infra/appops/go.mod` and Go module.
- [x] Create `infra/appops/Pulumi.yaml` project definition and `Pulumi.dev.yaml`.
- [x] Implement application discovery: scan `apps/*/appops.yaml` (`internal/discovery/discovery.go`).
- [x] Implement YAML config parsing and validation (`internal/config/config.go`).
- [x] Implement deterministic resource naming (`app_<name>_<env>`).
- [x] Add opt-in `apps/metrics-worker/appops.yaml`.
- [x] Add unit tests for discovery, parsing, validation, and naming (`internal/config/config_test.go`, `internal/discovery/discovery_test.go`).
- [x] Document the Pulumi state backend bootstrap procedure in `infra/appops/docs/STATE_BACKEND_BOOTSTRAP.md`.
- [x] Organize into standard Go layout (`cmd/appops/`, `internal/`, `docs/`).

**Verification status:** `go test -v ./...`, `go vet ./...`, and repository pre-commit checks pass cleanly.

### Milestone 2 — PostgreSQL provisioning for one application (Applied and verified)

**Goal:** `pulumi up` creates a schema, role, and grants in a test PostgreSQL database.

**Status:** Code is in `internal/postgres/postgres.go` and `main.go`. Applied together with Milestone 3 in one `pulumi up`; the schema and role exist in the live database.

- [x] Add PostgreSQL provider configuration (`TARGET_PG*` env vars).
- [x] Implement schema creation.
- [x] Implement role creation with `RandomPassword` (`SkipDropRole` set).
- [x] Use the write-only `passwordWo` / `passwordWoVersion` fields (provider upgraded to `pulumi-postgresql` v3.18.3; version comes from `credentials.version`).
- [x] Implement grants (CONNECT, USAGE, table/sequence privileges).
- [x] Implement default privileges for the migration owner (`postgres`).
- [x] Verify catalog state on the live database (2026-11-11): role can log in with no superuser/createrole/createdb/bypassrls; schema owned by `postgres`; role has CONNECT, schema USAGE (no CREATE); default privileges from `postgres` are `arwd` on tables and `rU` on sequences.
- [x] Verify behavior by connecting as the role: own-schema access and cross-schema isolation confirmed (2026-11-11).

**Known follow-ups:** add tests for `internal/postgres`; finish the connection-based verification.

**Testable independently:** Connect as the provisioned role; verify access to its own schema and denial of others.

### Milestone 3 — Cloudflare Secrets Store integration (Completed)

> [!WARNING]
> `pulumi-cloudflare` was upgraded to v6.23.3 (v6.2.0 had no `SecretsStoreSecret`). In that version `Value` is a secret-wrapped input, **not** write-only, so the connection URI is stored (encrypted) in Pulumi state. Confirm with `pulumi stack export` after the first apply.

**Status:** `internal/secrets/secrets.go` builds the percent-encoded URI and creates the secret after the role and grants. Required settings: the store ID (`CLOUDFLARE_SECRETS_STORE_ID` in `.env`, or `appops:secretsStoreId` in stack config; the store is created manually), plus `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` (or `appops:cloudflareAccountId`). Optional: `appops:appDbHost`, `appDbPort`, `appDbUserSuffix` (e.g. `.<project-ref>` for the Supabase pooler). Rotation: bump `credentials.version` in `appops.yaml`.

- [x] Add Cloudflare provider configuration and secret creation.
- [x] Set the store ID and complete the first `pulumi up` (needed the API token to have Account → Secrets Store → Edit).
- [x] Secret `APP_METRICS_WORKER_DEV_DATABASE_URL` exists in Secrets Store.
- [x] `pulumi stack export` checked with and without `--show-secrets`; secret values are encrypted in state (2026-11-11).
- [x] Idempotency: preview with unchanged `credentials.version` shows no changes (2026-11-11).
- [x] Rotation: bumping `credentials.version` to 2 and running `pulumi up` replaced the password, updated the role, and updated the Cloudflare secret; verified end to end (2026-11-11).

**Rotation recovery:** Postgres is updated before Cloudflare. If the Cloudflare step fails, the role already has the new password and the secret is stale. Fix the cause (token permission, network) and re-run `pulumi up`; it only needs to push the secret. Do not decrement `credentials.version` to "undo" a rotation; that generates yet another password.

**Goal:** The generated connection URI is stored in Cloudflare Secrets Store.

- Add Cloudflare provider configuration.
- Implement secret creation with the connection URI.
- Verify write-only behavior and idempotency.
- Document how an application retrieves its secret.

**Testable independently:** Verify the secret exists in Cloudflare; verify `pulumi up` with no changes produces no diff.

### Milestone 4 — Multi-application support and protection

**Goal:** Adding a second `appops.yaml` provisions only that application's resources.

- Extend the main loop to handle multiple discovered apps.
- Add deletion safeguards (retain on delete, or require explicit confirmation).
- Test isolation between applications.

### Milestone 5 — metrics-worker schema migration (separate)

**Goal:** Move metrics-worker tables from `public` to `metrics_worker`.

- This is a database migration, not an AppOps code change — it would be a new file in `db/supabase/migrations/`.
- Requires updating the `SECURITY DEFINER` functions.
- Requires a coordinated deployment: migration, then application config update.

### Milestone 6 — Documentation

- Onboarding guide, credential retrieval, rotation, recovery, state backup.

---

## 6. Files Expected in Milestone 1

### New files created in Milestone 1

| Path | Purpose |
|---|---|
| `infra/appops/go.mod` | Go module declaration |
| `infra/appops/go.sum` | Go dependency checksums |
| `infra/appops/Pulumi.yaml` | Pulumi project definition (configured for `cmd/appops`) |
| `infra/appops/Pulumi.dev.yaml` | Stack configuration for dev environment |
| `infra/appops/cmd/appops/main.go` | Pulumi program entry point |
| `infra/appops/internal/config/config.go` | Application config types, validation, and naming |
| `infra/appops/internal/config/config_test.go` | Unit tests for config parsing and validation |
| `infra/appops/internal/discovery/discovery.go` | Filesystem scanning for `appops.yaml` with opt-in and conflict checks |
| `infra/appops/internal/discovery/discovery_test.go` | Unit tests for discovery logic |
| `infra/appops/README.md` | Project documentation and directory structure |
| `infra/appops/docs/STATE_BACKEND_BOOTSTRAP.md` | State backend bootstrap runbook |
| `infra/appops/docs/RECONNAISSANCE.md` | Repository reconnaissance report |
| `apps/metrics-worker/appops.yaml` | Opt-in application configuration for metrics-worker |

### Files not modified

No existing files are modified in Milestone 1. The `appops.yaml` is a new file added to the metrics-worker directory.

### Files deferred to later milestones

| Path | Milestone | Purpose |
|---|---|---|
| `infra/appops/postgres.go` | 2 | PostgreSQL resource provisioning |
| `infra/appops/secrets.go` | 3 | Cloudflare Secrets Store integration |
| `deploy/units.toml` | 4+ | Add `appops` unit for CI integration |
| `db/supabase/migrations/<timestamp>_*.sql` | 5 | Schema migration for metrics-worker |
