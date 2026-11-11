# AppOps: Monorepo PostgreSQL Provisioning with Pulumi

## Objective

Build a small Go-based Pulumi project that automatically discovers applications in this monorepo and provisions PostgreSQL database identities and credentials for them.

The goal is to make onboarding a new application straightforward:

1. Add an AppOps configuration file to its directory under `apps/`.
2. Run `pulumi preview` and `pulumi up`.
3. AppOps creates an isolated PostgreSQL schema and application login role.
4. AppOps generates a secure password and stores the resulting connection credential in Cloudflare Secrets Store.
5. The application can retrieve its database connection credential through the appropriate Cloudflare integration.

This is a practical infrastructure automation tool, not a custom provider-development project.

## Important repository context

Known characteristics:

- Applications live under `apps/`.
- All application schema migrations currently live in one shared migration directory.
- There are not separate migration directories for individual applications.
- At least one application currently uses the `public` schema. The metrics-worker application can be migrated to its own schema.
- Supabase hosts the application PostgreSQL database.
- Applications should use ordinary PostgreSQL connection strings and credentials, not Supabase Auth identities.
- A separate PostgreSQL server will be used for Pulumi state storage.
- The user is interested in learning Pulumi and Go.

Do not reorganize the existing migration directory or create per-application migration directories as part of this project.

## Core architecture

Use:

- Pulumi Go SDK for orchestration.
- Existing Pulumi PostgreSQL provider for database resources.
- Existing Pulumi Cloudflare provider for Secrets Store resources.
- Pulumi PostgreSQL DIY backend for state storage.
- Pulumi Random provider for stable password generation.
- Go filesystem APIs for application discovery.
- YAML configuration files for application-specific provisioning settings.

Do not build:

- A custom Terraform or Pulumi provider.
- A Cloudflare Worker provisioning API.
- A D1 operation ledger.
- A custom HTTP provisioning service.
- A separate migration runner.

Prefer existing provider resources over custom infrastructure code.

## Repository discovery

Before implementation, inspect the existing monorepo and adapt to its conventions.

Identify:

- Existing applications and directory structure.
- The shared migration directory and its tooling.
- How applications currently configure database connections.
- The metrics-worker application's current schema and database usage.
- Existing Supabase configuration and local development setup.
- Existing Cloudflare infrastructure and account configuration.
- Existing Pulumi projects or Go modules.
- How application secrets are currently distributed.

Report findings and propose a file layout before making broad changes.

Do not assume the example directory names in this document match the actual repository.

## Pulumi state backend

Use Pulumi's PostgreSQL DIY backend for state storage.

The state database must be hosted on a **separate PostgreSQL server from the application database**.

The purpose is to keep infrastructure state independent of the resources being managed. AppOps must never manage the database that stores its own state.

### State backend architecture

There are three distinct storage destinations:

| System | Responsibility |
|---|---|
| Dedicated PostgreSQL state server | Pulumi state, deployment history, encrypted configuration and secrets |
| Supabase PostgreSQL instance | Application schemas, roles, grants, and application data |
| Cloudflare Secrets Store | Runtime database credentials consumed by applications |

Do not combine these responsibilities.

### Backend setup

Use Pulumi's PostgreSQL DIY backend:

```bash
pulumi login postgres://
```

Configure the backend connection through standard PostgreSQL environment variables or a documented connection URI.

For example:

```bash
export PGHOST=state-db.example.com
export PGPORT=5432
export PGUSER=pulumi_backend
export PGPASSWORD='<backend-password>'
export PGDATABASE=pulumi_state
export PGSSLMODE=require

pulumi login postgres://
```

These are examples; use the actual connection settings for the state server.

### State database provisioning

The state database must be provisioned independently of AppOps.

Document a manual bootstrap procedure or a separate infrastructure setup for:

- Creating the `pulumi_state` database.
- Creating a dedicated backend login role.
- Granting the required database privileges.
- Configuring network access and TLS.
- Establishing backups and recovery procedures.

Do not create the state database or its PostgreSQL server as resources in the AppOps Pulumi project.

The state backend is a prerequisite for running AppOps.

### State security

Pulumi DIY backends do not automatically provide the same managed secrets-encryption key handling as Pulumi Cloud.

Configure a passphrase-based secrets provider or another supported encryption provider.

Requirements:

- Set up `PULUMI_CONFIG_PASSPHRASE` through a secure environment mechanism.
- Never commit the passphrase.
- Never store it in the state database it protects.
- Keep an independent secure backup of the passphrase.
- Document recovery implications if the passphrase is lost.
- Use TLS for state database connections.
- Restrict network access to trusted development machines and CI runners.
- Use a dedicated backend database identity.
- Back up the state database independently of the application database.

Test that an existing stack can be restored from a backup.

### State locking and recovery

Use the backend's supported state locking and history mechanisms.

Document:

- How concurrent deployments are handled.
- How to inspect deployment history.
- How to back up and restore state.
- How to recover from an interrupted deployment.
- How to migrate the stack to another backend if necessary.

Do not assume that database backups alone are sufficient without testing an actual restoration.

## Application configuration

Each application that needs a database identity should contain an `appops.yaml` configuration file.

Example: `apps/blog/appops.yaml`

```yaml
name: blog

database:
  name: postgres
  schema: blog

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

Example: `apps/metrics-worker/appops.yaml`

```yaml
name: metrics-worker

database:
  name: postgres
  schema: metric_worker

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

### Configuration requirements

- `name` identifies the application.
- `database.name` identifies the PostgreSQL database.
- `database.schema` identifies the application's isolated schema.
- Permissions specify the application's runtime access.
- Credential rotation must be explicit.

Schema names should default to a sanitized version of the application name if omitted, but allow an explicit override.

Validate PostgreSQL identifier constraints, including the maximum identifier length.

Do not silently accept invalid or conflicting configurations.

Keep the initial configuration small.

## Application discovery

Implement a discovery function that scans the monorepo's `apps/` directory for application directories containing `appops.yaml`.

The discovery mechanism should:

1. Read immediate application directories under `apps/`.
2. Look for `appops.yaml` in each directory.
3. Parse and validate each configuration.
4. Return a collection of application definitions.
5. Produce useful errors that identify the file and invalid field.

Avoid hardcoding individual applications in `main.go`.

Do not recursively scan arbitrary directories unless the existing repository structure requires it.

The directory convention should make onboarding a new app as simple as adding its configuration file.

## PostgreSQL provisioning

Use the existing Pulumi PostgreSQL provider.

Documentation:

- https://www.pulumi.com/registry/packages/postgresql/
- https://github.com/pulumi/pulumi-postgresql

Use the Go SDK and native provider resources.

### Per-application resources

Each configured application should receive:

1. A PostgreSQL schema.
2. A PostgreSQL login role.
3. A generated password.
4. Database connection privileges.
5. Schema-specific runtime privileges.
6. Appropriate default privileges for future tables and sequences.

Use deterministic resource names so Pulumi can consistently reconcile resources between deployments.

Suggested PostgreSQL role naming:

`app_<application>_<environment>`

Examples:

- `app_blog_dev`
- `app_stats_dev`
- `app_metric_worker_dev`

The environment should come from Pulumi stack configuration or another clearly defined deployment setting.

Do not derive resource identities from random values.

### Schema isolation

**Each application gets its own PostgreSQL schema.**

Applications should not have access to other applications' schemas by default.

Examples:

| Application | Schema | Runtime role |
|---|---|---|
| blog | `blog` | `app_blog_dev` |
| stats | `stats` | `app_stats_dev` |
| metrics-worker | `metric_worker` | `app_metric_worker_dev` |

Do not grant runtime roles access to the entire `public` schema.

Do not grant cross-application access unless explicitly requested in a future configuration change.

Cross-application permissions are out of scope for the initial implementation.

### Database privileges

Distinguish between the migration identity and the runtime identity.

**Migration identity**

The shared migration process needs sufficient permissions to create and modify objects in the application schemas.

It should be a centrally managed identity, not one runtime role per application.

The migration identity may need:

- Database `CONNECT`.
- Schema `USAGE`.
- Schema `CREATE`.
- Appropriate ownership or DDL privileges.

Inspect the current migration execution model before deciding how ownership should be handled.

**Runtime identity**

The application's login role should have:

- Database `CONNECT`.
- Schema `USAGE`.
- Explicit table privileges.
- Explicit sequence privileges.
- No unnecessary DDL permissions.
- No role-management or privilege-escalation permissions.

For the initial implementation, support configurable table and sequence privileges.

Do not automatically make runtime roles schema owners.

### Default privileges

New tables and sequences must be accessible to the application's runtime role after future migrations.

PostgreSQL default privileges are associated with the role that creates the objects. They are not automatically global to a schema.

Inspect which role currently executes migrations and which role owns the created objects.

Configure default privileges for objects created by the appropriate migration identity.

Test this behavior by:

1. Provisioning an application role.
2. Creating a new table as the migration identity.
3. Connecting as the application runtime role.
4. Confirming the role can perform its intended operations.
5. Confirming the role cannot access another application's schema.

Do not implement default privileges without understanding object ownership.

## Credential generation and storage

Use the Pulumi Random provider's `RandomPassword` resource to generate stable passwords.

Do not generate a new password every time the Pulumi program executes.

Mark generated password outputs as secrets using Pulumi's secret mechanisms. Verify how the selected Random provider version stores generated values in state.

The same generated password must be used for:

- The PostgreSQL role's write-only password input.
- The Cloudflare Secrets Store secret value.

### PostgreSQL password resource

Use the PostgreSQL provider's write-only password field, `passwordWo`, and its associated `passwordWoVersion`.

This avoids exposing the password as a normal PostgreSQL resource output.

Use an explicit version to control when PostgreSQL receives a password update.

Documentation:

- https://www.pulumi.com/registry/packages/postgresql/api-docs/role/

### Cloudflare Secrets Store

Use the existing Pulumi Cloudflare provider.

Documentation:

- https://www.pulumi.com/registry/packages/cloudflare/
- https://developers.cloudflare.com/secrets-store/

Use account-level Secrets Store resources where appropriate.

Suggested secret naming convention:

`APP_<APPLICATION>_<ENVIRONMENT>_DATABASE_URL`

Examples:

- `APP_BLOG_DEV_DATABASE_URL`
- `APP_STATS_DEV_DATABASE_URL`
- `APP_METRIC_WORKER_DEV_DATABASE_URL`

The secret value should be a complete PostgreSQL connection URI where practical.

Example format:

`postgresql://app_blog_dev:<encoded-password>@<host>:<port>/postgres`

Ensure passwords are correctly percent-encoded when constructing connection URIs.

Keep the host, port, and database name configurable through Pulumi configuration rather than hardcoding them.

### Secret handling requirements

- Do not print passwords or connection strings.
- Do not commit generated credentials.
- Do not store passwords in application configuration files.
- Use Pulumi secret values for sensitive inputs and outputs.
- Protect Pulumi state with an encrypted secrets provider.
- Investigate the actual state behavior of each provider's write-only fields.
- Do not assume Cloudflare's write-only API means its value is absent from Pulumi state.
- Never expose generated passwords as ordinary stack outputs.

The application should consume its credential through Cloudflare's supported secret retrieval mechanism.

Document how an application obtains access to its own secret, including any required service token, scope, or Cloudflare binding configuration.

Do not grant every application access to every secret.

### Cloudflare write-only secret behavior

The Cloudflare Secrets Store secret resource has a write-only value input.

Verify the selected provider version's behavior when:
- Creating a secret.
- Running a subsequent unchanged deployment.
- Updating a secret value.
- Replacing a secret.
- Recovering after an interrupted deployment.

Because the value is write-only, do not assume Pulumi can read it back or detect arbitrary changes to it.

### Credential rotation

Credential rotation must be deliberate.

The initial configuration can use:

```yaml
credentials:
  rotate: false
```

Do not generate a new password on every Pulumi deployment.

Design a mechanism for explicitly requesting rotation, such as a version or rotation token.

A rotation must update the PostgreSQL role password and the Cloudflare secret consistently.

Consider the failure case where PostgreSQL password rotation succeeds but the Cloudflare secret update fails.

Document recovery behavior and avoid silently reporting success.

Do not declare automatic rotation complete until the provider lifecycle has been tested end to end.

## Pulumi resource lifecycle

Use deterministic resource names and normal Pulumi reconciliation behavior.

Expected behavior:

| Change | Expected result |
|---|---|
| New `appops.yaml` | Create schema, role, grants, and secret |
| No configuration changes | No resource changes |
| Add a permission | Update grants |
| Remove a permission | Revoke the corresponding grant |
| Change schema | Apply a deliberate migration/replacement strategy |
| Remove `appops.yaml` | Remove managed resources, subject to deletion safeguards |
| Request password rotation | Rotate password and update secret |

Be particularly careful with deletion.

Dropping a PostgreSQL role can break an application immediately.

Use appropriate protection or safeguards to prevent accidental deletion of application roles and schemas.

Do not use destructive behavior as an implicit consequence of a typo or temporary configuration omission.

Document the behavior of deleting an application configuration file.

## Shared migration workflow

Preserve the existing shared migration directory.

AppOps is responsible for provisioning application schemas and identities. It is not a migration runner.

The migration process remains centrally managed.

The intended workflow is:

1. AppOps provisions the schema and roles.
2. The existing migration workflow applies the shared migrations to the intended schemas.
3. Application runtime roles connect only to their own schema.

Inspect the existing migration tooling to determine how it can target multiple schemas.

Do not invent a new migration mechanism or restructure the migration directory.

If the existing migration tooling cannot target multiple schemas cleanly, document the limitation and propose the smallest compatible change.

## metrics-worker migration

The metrics-worker application currently uses `public`.

As part of the project, determine what is required to move it to its own `metric_worker` schema.

Inspect:

- Existing tables and sequences.
- Existing migration definitions.
- Queries that assume `public`.
- Database connection configuration.
- Any Supabase-specific schema configuration.
- Existing grants and object ownership.

Propose a safe migration plan.

Do not move or rename existing database objects without documenting the impact and providing a reversible or recoverable migration strategy.

The application should eventually use its dedicated schema and role.

## Suggested repository layout

Adapt this to the existing repository:

```text
repo/
├── apps/
│   ├── blog/
│   │   ├── appops.yaml
│   │   └── ...
│   ├── stats/
│   │   ├── appops.yaml
│   │   └── ...
│   ├── metrics-worker/
│   │   ├── appops.yaml
│   │   └── ...
│   └── migrations/
│       └── ...
│
└── infra/
    └── appops/
        ├── Pulumi.yaml
        ├── Pulumi.dev.yaml
        ├── go.mod
        ├── main.go
        ├── config.go
        ├── discovery.go
        ├── postgres.go
        ├── secrets.go
        ├── README.md
        └── ...
```

Keep state backend bootstrap documentation separate from the AppOps deployment itself.

## Implementation phases

### Phase 1: Repository assessment (Completed)

Inspect the monorepo and report:

- [x] Existing app layout.
- [x] Migration directory and tooling.
- [x] Current database schema and ownership conventions.
- [x] metrics-worker's use of `public`.
- [x] Existing Cloudflare setup.
- [x] Existing Pulumi or Go conventions.

Propose the implementation file layout and identify blockers. Report generated at `infra/appops/docs/RECONNAISSANCE.md`.

### Phase 2: Bootstrap and Pulumi project (In Progress)

Document and validate the independent PostgreSQL state backend.

- [x] State backend setup documented in `infra/appops/docs/STATE_BACKEND_BOOTSTRAP.md`.
- [x] Pulumi Go project created and configured for DIY PostgreSQL state backend (`infra/appops/Pulumi.yaml`, `go.mod`).
- [ ] User provisions dedicated PostgreSQL state server and runs `pulumi login postgres://`.

### Phase 3: Discovery and configuration (Completed)

Implement:

- [x] Application discovery (`internal/discovery/discovery.go`).
- [x] YAML parsing (`internal/config/config.go`).
- [x] Configuration validation.
- [x] Deterministic resource naming (`app_<name>_<env>`).
- [x] Schema and permission defaults.
- [x] Add unit tests for discovery and validation (`internal/config/config_test.go`, `internal/discovery/discovery_test.go`).

### Phase 4: Provision one application

Implement one complete vertical slice for a single application:

1. Create schema.
2. Generate stable password using `RandomPassword`.
3. Create login role using the PostgreSQL write-only password field.
4. Grant runtime permissions.
5. Configure default privileges.
6. Store the connection URI in Cloudflare Secrets Store.

Verify that the application can connect using the generated credential.

Verify that it cannot access another application's schema.

Verify that secrets do not appear in plaintext in exported state.

### Phase 5: Multiple applications

Extend discovery to provision every configured application.

Verify that adding a new app configuration creates only that application's resources.

Verify that changes to one application's permissions do not modify other applications' grants.

### Phase 6: metrics-worker migration

Move metrics-worker from `public` to its dedicated schema.

Update its application configuration and connection settings.

Verify its normal functionality after migration.

### Phase 7: Documentation

Document:

- How to add an application.
- How to configure database permissions.
- How to run preview and deployment.
- How to retrieve application credentials.
- How to rotate credentials.
- How to migrate an application to a different schema.
- How to recover from partial provisioning failures.
- How to safely remove an application.
- How to bootstrap and access the state backend.
- How to restore state from backup.
- Known limitations.

## Testing expectations

At minimum, test:

- Discovery of valid application configurations.
- Handling of missing or malformed configurations.
- Schema-name validation.
- Role-name generation.
- Password generation and stability.
- Permission validation.
- Deterministic resource naming.
- No unnecessary changes on repeated deployments.
- Permission additions and revocations.
- Explicit credential rotation.
- Secret values not appearing in logs or ordinary stack outputs.
- Secret encryption in exported Pulumi state.
- Runtime role access to its own schema.
- Runtime role denial when accessing another application's schema.
- Access to tables created after role provisioning.
- Protection against accidental deletion.
- State backup and restoration.

Use local PostgreSQL where possible for permission integration tests.

## Scope boundaries

The first implementation should not include:

- Cross-application schema permissions.
- Automatic schema migration execution.
- A custom provider.
- A centralized provisioning API.
- A Worker or D1 backend.
- A generic secrets management platform.
- Automatic application deployment.
- Automatic credential injection into every application runtime.
- AppOps-managed provisioning of its own state database.

These can be considered later if the project demonstrates a real need.

## Working instructions for the implementing LLM

Work incrementally.

1. Inspect the repository before choosing implementation details.
2. Report the existing conventions and proposed changes.
3. Verify current Pulumi provider resource APIs and Go SDK signatures against documentation.
4. Implement one application end to end before generalizing.
5. Validate the PostgreSQL DIY backend and encrypted state configuration independently.
6. Run formatting, unit tests, and available integration tests.
7. Keep the implementation small and understandable.
8. Do not claim secrets are absent from state without verifying provider behavior.
9. Do not perform destructive schema or role changes without explaining the consequences first.
10. Preserve the shared migration directory and its existing workflow.
11. Clearly identify prototype limitations and follow-up work.

The desired outcome is a simple, repeatable developer experience: add an application configuration file, run Pulumi, and receive an isolated PostgreSQL identity with its credential securely published to Cloudflare.

The infrastructure state itself should live on a separate, independently managed PostgreSQL server.
