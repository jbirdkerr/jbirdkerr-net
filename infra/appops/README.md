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

Applications without `appops.yaml` are ignored.

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
    ├── IMPLEMENTATION.md        # Architecture specification
    ├── PREWORK.md               # Reconnaissance checklist
    ├── RECONNAISSANCE.md        # Reconnaissance report and milestone status
    └── STATE_BACKEND_BOOTSTRAP.md # PostgreSQL DIY state backend guide
```

## Running Tests

Run unit tests locally:

```bash
cd infra/appops
go test -v ./...
```

## Rotating an Application's Credential

Increment `credentials.version` in the app's `appops.yaml`, then run `./run.sh preview` and `./run.sh up`.
This regenerates the password, updates the PostgreSQL role (`passwordWo`), and updates the Cloudflare secret.
PostgreSQL is updated first; if the Cloudflare step fails, fix the cause and re-run `up`. Do not decrement the
version to undo a rotation.

## Removing an Application

Deleting an `appops.yaml` (or a typo that makes discovery skip it) must not drop a live role or schema, so the
schema and role are created with `Protect`. Behavior:

- `./run.sh preview` fails with a protected-resource error for that app. No resources are deleted.
- To remove the app deliberately, first confirm nothing uses its role or schema, then unprotect that app's schema and
  role (`./run.sh state unprotect '<urn>'`; list URNs with `./run.sh stack --show-urns`), then run `up`.
- The role is created with `SkipDropRole`, so Pulumi stops tracking it but PostgreSQL keeps it; drop it manually.
  A non-empty schema is not dropped automatically.
- The Cloudflare secret is not protected and is deleted with the app. Remove any worker bindings that use it first.

This procedure has not been exercised against a live database yet.

## Reserved Schemas

`database.schema` may not be `public`, `pg_*`, `information_schema`, or a Supabase-managed schema
(`auth`, `storage`, `realtime`, `extensions`, `vault`, ...). Two apps whose names sanitize to the same identifier
(for example `my-app` and `my_app`) are rejected as a conflict.
