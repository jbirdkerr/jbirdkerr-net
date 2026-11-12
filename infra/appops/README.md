# AppOps

Automated PostgreSQL application role and schema provisioning with Cloudflare Secrets Store integration.

## Overview

AppOps automatically discovers applications inside `apps/` that opt in by declaring an `appops.yaml` file. It provisions:
1. An isolated PostgreSQL database schema.
2. A deterministic application login role (`app_<appname>_<env>`).
3. Strong generated passwords via Pulumi's `RandomPassword`.
4. Least-privilege object grants and default privileges for future migrations.
5. Secure connection URI publication to Cloudflare Secrets Store (`APP_<APPNAME>_<ENV>_DATABASE_URL`).

## Opting In an Application

To give any application its own database schema and credentials, add `appops.yaml` to its root directory under `apps/`:

```yaml
name: my-app

database:
  name: postgres
  schema: my_app

permissions:
  tables:
    - SELECT
    - INSERT
    - UPDATE
    - DELETE
  sequences:
    - USAGE
    - SELECT

credentials:
  version: 1   # bump to rotate the password
```

Applications without `appops.yaml` are ignored. `credentials.version` is the explicit rotation token (default 1). Reserved schemas (`public`, `pg_*`, `information_schema`, Supabase-managed schemas) are rejected, and two apps whose names sanitize to the same identifier (for example `my-app` and `my_app`) conflict.

## Quick Start

```bash
cd infra/appops
cp .env.example .env     # fill in; see docs/OPERATIONS.md section 1
./run.sh login
./run.sh stack select dev
./run.sh preview         # plan only
./run.sh up              # apply
```

## Documentation

| Document | Contents |
|---|---|
| [docs/OPERATIONS.md](./docs/OPERATIONS.md) | Setup, adding an app, permissions, preview/deploy, retrieving and rotating credentials, schema changes, failure recovery, state backup/restore, removal, known limitations. Each procedure is marked verified or untested. |
| [docs/STATE_BACKEND_BOOTSTRAP.md](./docs/STATE_BACKEND_BOOTSTRAP.md) | Provisioning the separate PostgreSQL state backend |
| [docs/design/IMPLEMENTATION.md](./docs/design/IMPLEMENTATION.md) | Architecture specification and phase status |
| [docs/design/RECONNAISSANCE.md](./docs/design/RECONNAISSANCE.md) | Repository findings and milestone status |
| [docs/design/PLAN_PHASE_6_7.md](./docs/design/PLAN_PHASE_6_7.md) | The metrics-worker schema move and its rehearsal/rollback SQL |


## State Backend

AppOps uses Pulumi's PostgreSQL DIY backend. The backend must be configured and running on a separate PostgreSQL database from the application database.

See [docs/STATE_BACKEND_BOOTSTRAP.md](./docs/STATE_BACKEND_BOOTSTRAP.md) for full instructions on bootstrap, TLS, credentials, passphrase encryption, and disaster recovery.

## Architecture & Code Layout

```text
infra/appops/
├── Pulumi.yaml                  # Pulumi project configuration
├── Pulumi.dev.yaml              # Stack configuration (dev)
├── go.mod / go.sum              # Go dependencies
├── main.go                      # Pulumi entrypoint
├── run.sh                       # Loads .env and runs pulumi
├── .env.example                 # Required environment variables (copy to .env)
├── README.md                    # Project overview
├── internal/
│   ├── config/                  # appops.yaml parsing & validation
│   ├── discovery/               # App discovery & conflict detection
│   ├── postgres/                # Schema, role, grants, default privileges
│   └── secrets/                 # Cloudflare Secrets Store publishing
└── docs/
    ├── OPERATIONS.md            # Runbook: setup, rotation, recovery, removal
    ├── STATE_BACKEND_BOOTSTRAP.md # PostgreSQL DIY state backend guide
    ├── phase6/                  # Rehearsal and rollback SQL for the metrics-worker schema move
    └── design/                  # Documents used to build the feature
        ├── IMPLEMENTATION.md    # Architecture specification and phase status
        ├── PREWORK.md           # Reconnaissance checklist
        ├── RECONNAISSANCE.md    # Reconnaissance report and milestone status
        └── PLAN_PHASE_6_7.md    # metrics-worker schema move plan
```

## Running Tests

Run unit tests locally:

```bash
cd infra/appops
go test -v ./...
```
