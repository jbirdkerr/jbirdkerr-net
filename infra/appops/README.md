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
  rotate: false
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
├── README.md                    # Project overview
├── cmd/
│   └── appops/
│       └── main.go              # CLI & Pulumi entrypoint
├── internal/
│   ├── config/
│   │   ├── config.go            # appops.yaml parsing & identifier validation
│   │   └── config_test.go       # Schema and permission unit tests
│   └── discovery/
│       ├── discovery.go         # App discovery & conflict resolution
│       └── discovery_test.go    # Discovery filter & conflict tests
└── docs/
    ├── IMPLEMENTATION.md        # Architecture specification
    ├── PREWORK.md               # Reconnaissance checklist
    ├── RECONNAISSANCE.md        # Initial reconnaissance report
    └── STATE_BACKEND_BOOTSTRAP.md # PostgreSQL DIY state backend guide
```

## Running Tests

Run unit tests locally:

```bash
cd infra/appops
go test -v ./...
```
