# AGENTS.md

AppOps: a Pulumi (Go) project that discovers `apps/*/appops.yaml` and provisions, per app, a PostgreSQL schema, login role, grants and default privileges on the Supabase database, and publishes the connection URI to Cloudflare Secrets Store. Read [`README.md`](README.md) and [`docs/OPERATIONS.md`](docs/OPERATIONS.md) before changing anything. Repository-wide rules are in the root [`AGENTS.md`](../../AGENTS.md).

This unit changes **real infrastructure** (the production Supabase database and Cloudflare). It is applied by hand with `./run.sh`; CI does not deploy it.

## Hard rules

- Do not run `./run.sh` or `pulumi` commands unless explicitly asked. That includes `preview`, which reads live state, and above all `up`, `refresh`, `destroy`, `cancel`, `import` and `state` subcommands, which change the database, Cloudflare, or the state. Never run them on your own initiative.
- Never print secrets: no `stack export --show-secrets`, no `config ... --show-secrets`, no reading or echoing `.env`, and no passwords or connection strings in docs, PR text, logs or tests.
- `.env` is gitignored. `.env.example` holds placeholders only.
- Keep `Protect` on schemas and roles and `SkipDropRole` on roles. Do not relax a safeguard unless asked, and explain the consequences first.
- AppOps must never manage the database that stores its own state.
- Resource names are part of the state. Changing the `<app>-<env>-<kind>` names or a role/secret naming rule replaces live resources. Treat that as a breaking change.
- Do not edit applied migrations. The migration `20261111180000_move-metrics-worker-schema.sql` points at `docs/phase6/rollback.sql`, so those files stay where they are.

## Layout

- `main.go`: Pulumi program (stack config, providers, per-app loop).
- `internal/config`: `appops.yaml` parsing, validation, naming.
- `internal/discovery`: scans `apps/*`, rejects conflicts.
- `internal/postgres`: schema, role, password, grants, default privileges.
- `internal/secrets`: connection URI and Secrets Store secret.
- `docs/`: `OPERATIONS.md` and `STATE_BACKEND_BOOTSTRAP.md` at the top level; design history under `docs/design/`; `docs/phase6/` holds rehearsal and rollback SQL.

## Conventions

- Role `app_<name>_<env>`; secret `APP_<NAME>_<ENV>_DATABASE_URL`; resources `<app>-<env>-<kind>`.
- Rotation is explicit through `credentials.version`. Never regenerate passwords implicitly.
- Provider versions in `go.mod` matter: `pulumi-postgresql` v3.18+ for `passwordWo`, `pulumi-cloudflare` v6.23+ for `SecretsStoreSecret`. Verify new resource arguments against the provider's SDK source or docs before using them.
- Keep the `appops.yaml` schema small. A new option needs validation, a test, and an `OPERATIONS.md` update.
- Do not claim a secret is absent from state without checking the provider's behavior. The Cloudflare secret value is stored (encrypted) in state.

## Validation

Run from `infra/appops`:

```bash
gofmt -l .          # must print nothing
go vet ./...
go test ./...
```

Root `uv run pre-commit run --all-files` covers the Markdown and YAML checks. CI does **not** run the AppOps Go checks, so run them yourself. Tests use Pulumi mocks and need no database.

## Documentation

Mark each procedure in `docs/OPERATIONS.md` as verified, partly verified, or untested, and do not mark one verified unless it was actually run against the real system. When handing off, say which checks ran and what was not exercised against the live database.
