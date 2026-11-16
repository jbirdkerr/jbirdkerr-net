# AppOps Operations Guide

How to set up, run and maintain AppOps. For what AppOps is, see the [README](../README.md); for the design, see [IMPLEMENTATION.md](./design/IMPLEMENTATION.md).

Each procedure is marked **Verified** (exercised against the live database), **Partly verified**, or **Untested** (written from the design and provider behavior, never run). Treat untested procedures as a starting point and rehearse them before relying on them.

## 1. One-time setup

1. **State backend.** Provision the separate PostgreSQL state server and database by following [STATE_BACKEND_BOOTSTRAP.md](./STATE_BACKEND_BOOTSTRAP.md). AppOps never creates or manages it.
2. **Local environment.** Copy `.env.example` to `.env` (it is gitignored) and fill it in:

   | Variable | Purpose |
   |---|---|
   | `PULUMI_STATE_PG*` | Connection to the state server (host, port, database, user, password, sslmode). `run.sh` builds `PULUMI_BACKEND_URL` from these. |
   | `PULUMI_CONFIG_PASSPHRASE` | Encrypts secrets in state. Never commit it; keep an independent copy (see §9). |
   | `TARGET_PG*` | Admin connection to the **application** database (where schemas and roles are created). |
   | `CLOUDFLARE_API_TOKEN` | Needs **Account → Secrets Store → Edit** on the account. A token without it fails with `403 Authentication error` during `up` (not during `preview`). |
   | `CLOUDFLARE_ACCOUNT_ID` | Account that owns the Secrets Store. |
   | `CLOUDFLARE_SECRETS_STORE_ID` | The store, created once by hand (`wrangler secrets-store store create`). |

3. **Log in and select the stack:**

   ```bash
   cd infra/appops
   ./run.sh login          # logs in to the PostgreSQL state backend
   ./run.sh stack select dev
   ```

4. **Optional stack config** (`./run.sh config set appops:<key> <value>`). Values fall back to the environment where noted.

   | Key | Default | Meaning |
   |---|---|---|
   | `environment` | `dev` | Suffix in role and secret names |
   | `migrationOwner` | `postgres` | Role that creates migrated objects; default privileges are granted for objects it creates |
   | `targetPgHost` | `TARGET_PGHOST` / `PGHOST` | Admin connection host |
   | `appDbHost`, `appDbPort` | admin host/port | Host and port written into each app's connection URI |
   | `appDbUserSuffix` | none | Appended to the role name in the URI, e.g. `.<project-ref>` for the Supabase pooler |
   | `cloudflareAccountId`, `secretsStoreId` | the `CLOUDFLARE_*` variables above | Secrets Store target |

Status: **Verified** (this is how the dev stack was set up).

## 2. Adding an application

1. Create `apps/<app>/appops.yaml` (see the README for the format). Names must be unique after sanitizing (`my-app` and `my_app` conflict), and the schema must not be reserved (`public`, `pg_*`, Supabase-managed schemas).
2. From `infra/appops`: `go test ./...`, then `./run.sh preview`. A new app should show only creates for that app (schema, password, role, grants, default privileges, secret) and no changes to existing apps.
3. `./run.sh up`.
4. Verify in the SQL Editor (replace the names):

   ```sql
   select rolname, rolcanlogin, rolsuper, rolcreaterole, rolcreatedb, rolbypassrls
   from pg_roles where rolname = 'app_<app>_dev';

   select has_database_privilege('app_<app>_dev', current_database(), 'CONNECT') as can_connect,
          has_schema_privilege('app_<app>_dev', '<schema>', 'USAGE')  as usage_own_schema,
          has_schema_privilege('app_<app>_dev', '<schema>', 'CREATE') as create_own_schema;
   ```

   Expect login true, no superuser/create-role/create-db/bypass-RLS, CONNECT and USAGE true, CREATE false. Also confirm `APP_<APP>_DEV_DATABASE_URL` exists in the Secrets Store.
5. **Migrations.** AppOps creates the schema and role; it does not create tables. Add a normal timestamped migration in `db/supabase/migrations/` that uses `CREATE SCHEMA IF NOT EXISTS <schema>` and qualifies every object with the schema. On this repository, merging to `main` applies migrations through the **Supabase GitHub integration**. The `SUPABASE_ACCESS_TOKEN not set; skipping` line in the CI log means the workflow's own `supabase db push` step skipped; the integration applied the migration anyway (confirmed for the metrics-worker migration with `supabase migration list --remote`). The integration and the CI deploy are independent, so do not assume the migration lands before a dependent worker deploy.
6. **Apps that talk to Supabase over the REST API (PostgREST)** also need: the schema exposed on the hosted project (Data API → Exposed schemas, or `alter role authenticator set pgrst.db_schemas = '...'` plus `notify pgrst, 'reload config'`), the schema added to `[api] schemas` in `db/supabase/config.toml`, and `Accept-Profile` / `Content-Profile` headers in the app. Developers with an existing local database must follow the one-time local reset in the Phase 6 section of [PLAN_PHASE_6_7.md](./design/PLAN_PHASE_6_7.md). Apps that connect directly with their AppOps credential need none of this.

Status: **Verified** for metrics-worker. **Untested** with a second application (Phase 5 live verification is deferred; mock-based tests cover resource isolation).

## 3. Configuring database permissions

`permissions.tables` accepts `SELECT`, `INSERT`, `UPDATE`, `DELETE`, `TRUNCATE`, `REFERENCES`, `TRIGGER`; `permissions.sequences` accepts `USAGE`, `SELECT`, `UPDATE`. Edit the lists in `appops.yaml`, then preview and apply. Adding a permission grants it. Removing one revokes it. Only that app's grant resources change; other apps are untouched.

Each app gets:
- `CONNECT` on the database and `USAGE` (never `CREATE`) on its own schema.
- The configured privileges on existing tables and sequences in its schema.
- **Default privileges** for tables and sequences later created by `migrationOwner` in that schema. They do not apply to objects created by other roles, and they do not apply to objects *moved* into the schema from elsewhere, because a move keeps the object's existing privileges.

If tables are moved into an app's schema (as with metrics-worker), re-apply the grants with `./run.sh refresh` then `./run.sh up`, then confirm:

```sql
select has_table_privilege('app_<app>_dev', '<schema>.<table>', 'SELECT') as can_select;
```

Status: configuring permissions **Verified** (catalog and probe-table checks on the live database). The refresh-and-up re-grant after a move is **verified once**: after the metrics-worker move, `./run.sh refresh` then `up` restored the role's access (`has_table_privilege` returned true, 2026-11-12). If `refresh` does not show a missing grant in another case, run the `GRANT`s once as the admin user instead.

### Function permissions and API tokens

Both options are off by default; apps without them get exactly the resources described above. Background and decisions: [API_CREDENTIALS_PLAN.md](./design/API_CREDENTIALS_PLAN.md).

```yaml
permissions:
  functions:
    - EXECUTE            # the only allowed value
api:
  enabled: true          # default false
  issued_on: 2026-11-13  # required when enabled; YYYY-MM-DD, not in the future
  expires_in_days: 180   # default 180; allowed 7..365
```

- `permissions.functions: [EXECUTE]` grants `EXECUTE` on all functions in the app's schema (`<app>-<env>-function-grants`) and on functions `migrationOwner` creates there later (`<app>-<env>-default-function-privs`). Apps that call RPCs need it, because the migrations revoke `EXECUTE` from `PUBLIC`.
- `api.enabled: true` makes `authenticator` a member of the app role (`<app>-<env>-api-authenticator`, no admin option) and publishes an ES256 token whose `role` claim is the app role to `APP_<NAME>_<ENV>_API_TOKEN` (`<app>-<env>-api-token`, `workers` scope). The token is issued at midnight UTC on `issued_on` and expires `expires_in_days` later. The expiry is exported as `apiTokenExpiresAt-<app>` (`./run.sh stack output`).
- The token runs as the app role: it can do what that role's grants allow in its own schema and nothing else, and it does **not** bypass RLS. Tables whose policies cover only `anon` and `authenticated` return no rows to it.
- The worker sends the token as `Authorization: Bearer <token>` with the project's publishable key as `apikey`.
- Removing an option removes its grants and secret on the next `up`.

**Signing key.** An app with `api.enabled` requires `SUPABASE_JWT_SIGNING_JWK` in `.env`: the P-256 private JWK imported into Supabase, as one line of JSON with its `kid`. If it is missing or invalid, the run stops before any resource is registered. It is read only when an app needs it and is never logged. This key can mint a token for **any** role, including `service_role`; treat `.env` accordingly. One-time setup (generating, importing and activating the key in the Supabase dashboard) is in plan section 6, and must be done before the first app enables `api`.

**Rotating or renewing a token.** Set `api.issued_on` to today's date, preview, `up`. Only the token secret changes; the database password does not. A full credential rotation means bumping `credentials.version` and updating `issued_on`. A run with no config change leaves the token as is. AppOps warns when a token expires within 30 days and refuses to publish one that has already expired.

**Kill switch.** Set `api.enabled: false` and `up` (removes the membership and the secret), or run `REVOKE app_<app>_<env> FROM authenticator;` as the admin user for an immediate stop. A token stays cryptographically valid until it expires; what stops it working is the removed membership.

Status: **Untested** against the live database and Cloudflare; covered by unit tests with Pulumi mocks. The `authenticator` membership, `EXECUTE` grant and ES256 tokens were exercised on a local Supabase stack (plan section 8), not through AppOps. Whether the provider's empty `objects` list covers all functions in the schema, as it does for tables and sequences, is unverified.

## 4. Previewing and deploying

```bash
cd infra/appops
./run.sh preview      # plan only; makes no changes
./run.sh up           # apply
./run.sh refresh      # re-read real infrastructure into state
```

A second `preview` with no `appops.yaml` changes should show no changes. A secret edited by hand in Cloudflare is **not** detected, because the Cloudflare value is write-only and Pulumi cannot read it back.

Status: **Verified**.

## 5. Retrieving credentials

- **Operators.** The Cloudflare dashboard does not show secret values. To read a connection string you must pull it from state, which prints secrets, so do it only on a trusted terminal and never paste the output: `./run.sh stack export --show-secrets`. Plain `stack export` shows only ciphertext.
- **Applications.** Bind the secret in the worker's `wrangler.toml` and read it asynchronously:

  ```toml
  [[secrets_store_secrets]]
  binding = "DATABASE_URL"
  store_id = "<store id>"
  secret_name = "APP_<APP>_DEV_DATABASE_URL"
  ```

  ```python
  db_url = await self.env.DATABASE_URL.get()   # not a plain string on env
  ```

  Secrets Store bindings are not available under `wrangler dev`; use `.dev.vars` locally. Rotating the secret needs no worker redeploy.

Status: the secrets exist and are encrypted in state (**Verified**). The binding and the `.get()` call are **Untested**: no worker consumes the credential yet. The metrics-worker still uses the Supabase REST API with the service-role key. Switching it to a direct Postgres connection (Hyperdrive or similar) has not been evaluated.

**Limitation:** a secret's `workers` scope limits which Cloudflare services may use it, not which worker. Any worker in the account that binds to this store and secret name can read it.

## 6. Rotating credentials

Increment `credentials.version` in the app's `appops.yaml`, then preview and `up`. This regenerates the password, updates the PostgreSQL role through `passwordWo` / `passwordWoVersion`, and updates the Cloudflare secret. Do not lower the version to "undo" a rotation: that generates yet another password.

PostgreSQL is updated before Cloudflare. If the Cloudflare step fails, the role already has the new password and the secret is stale; fix the cause and re-run `up`.

Status: **Verified** end to end (version 1 to 2, then idempotent preview).

## 7. Changing an application's schema

Changing `database.schema` replaces the schema resource. Because schemas are `Protect`ed, `preview` fails with a protected-resource error, which is the intended safeguard. A suggested order, never run:

1. Add a migration that creates the new schema (`IF NOT EXISTS`) and moves the objects into it, like the metrics-worker migration (`ALTER TABLE ... SET SCHEMA`, then recreate functions so bodies and `search_path` point at the new schema).
2. Unprotect the old schema: `./run.sh state unprotect '<urn>'` (list URNs with `./run.sh stack --show-urns`).
3. Change `database.schema`, run `preview`, and read the plan: expect the schema, its grants and its default privileges to be replaced. A non-empty old schema is not dropped (no cascade), so move everything out first.
4. `up`, then re-run the checks in §2 for the new schema.
5. Update schema exposure and the app's profile headers if it uses PostgREST.

Status: **Untested** as a procedure. The only schema move so far created the schema through AppOps first and moved tables in; see [PLAN_PHASE_6_7.md](./design/PLAN_PHASE_6_7.md) for how that went.

## 8. Recovering from partial failures

| Situation | What to do |
|---|---|
| `up` fails creating the Cloudflare secret (for example `403 Authentication error`) | Fix the token (needs Account → Secrets Store → Edit) and re-run `up`. The schema, role and grants already exist in state, so only the secret is created. **Verified.** |
| Rotation: role updated, secret update failed | Fix the cause and re-run `up`; do not change `credentials.version`. |
| `up` interrupted (network, Ctrl-C) or reports a held lock | `./run.sh cancel`, then `./run.sh refresh`, then `./run.sh preview`. **Untested.** |
| A resource was changed outside Pulumi | `./run.sh refresh` reads PostgreSQL changes into state. Cloudflare secret values are write-only and cannot be refreshed; rotate to overwrite them. |
| `preview` refuses to delete a protected resource | A discovered app disappeared (deleted file, typo, wrong directory). Restore the file. See §10 for deliberate removal. |
| State lost or corrupted | See §9. |

## 9. State backend: access, backup and restore

- **Access:** `./run.sh login` uses the `PULUMI_STATE_PG*` variables. The state lives on a separate database from the application data; the example environment points it at a dedicated Supabase project.
- **Locking and history:** the PostgreSQL backend serializes concurrent updates. Inspect history with `./run.sh stack history`.
- **Backups:** `pg_dump --format=custom` of the state database. The guide's backup, restore, interrupted-deployment and backend-migration commands are in [STATE_BACKEND_BOOTSTRAP.md](./STATE_BACKEND_BOOTSTRAP.md). Confirm what your hosting plan actually retains; that has not been checked.
- **Passphrase:** `PULUMI_CONFIG_PASSPHRASE` decrypts every secret in state. Keep a copy outside the state database (password manager). If it is lost, the encrypted values in state (generated passwords, secret values) can no longer be read. The databases and Cloudflare secrets keep working, but you would have to rebuild the stack and rotate credentials to regain control. This is the expected behavior and has not been tested.
- **Restore test (outstanding).** The specification requires proving a restore, not assuming it. Run this once and record the date here:
  1. `pg_dump --format=custom` the state database.
  2. Restore into a scratch database on a scratch server.
  3. Point a shell at the scratch database and run `./run.sh login`, `./run.sh stack select dev`, and `./run.sh preview`. The preview should show **no changes** when the restored state matches the live infrastructure. Do not run `up` against the scratch state.

Status: backups and restore are **Untested**.

## 10. Removing an application

Deleting an `appops.yaml` (or a typo that makes discovery skip it) must not drop a live role or schema, so schemas and roles are `Protect`ed.

- `./run.sh preview` fails with a protected-resource error for that app. Nothing is deleted.
- To remove an app deliberately: confirm nothing uses its role, schema or secret, then unprotect that app's schema and role (`./run.sh state unprotect '<urn>'`), then `up`.
- The role uses `SkipDropRole`: Pulumi forgets it but PostgreSQL keeps it, so drop it by hand. A non-empty schema is not dropped.
- The Cloudflare secret is not protected and is deleted with the app. Remove worker bindings that use it first.

Status: **Untested** against the live database. Rehearse with a throwaway app first.

## 11. Known limitations

- The Cloudflare secret value is stored (encrypted) in Pulumi state; the provider's `value` is not write-only in state. The PostgreSQL password uses the write-only field.
- Cloudflare secret drift is undetectable.
- A secret is readable by any worker in the account that binds to it.
- Only the `dev` environment has been exercised.
- Cross-app permissions, automatic rotation, migration execution and automatic credential injection are out of scope.
- The metrics-worker does not use its AppOps credential or an API token yet; it stays on the shared service-role key.
- API tokens expire and AppOps is applied by hand: if nobody updates `api.issued_on` and runs `up` before the exported expiry, the app's REST calls fail.
- The `app_..._dev` role needs its grants re-applied (`refresh` then `up`) after tables are moved into its schema (§3).
- Phase 5 (live second app), app removal, schema change and state restore have not been exercised against real infrastructure.
