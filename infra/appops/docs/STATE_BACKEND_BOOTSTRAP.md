# AppOps — Pulumi State Backend Bootstrap Guide

This guide covers setting up the **dedicated PostgreSQL server** that stores Pulumi state for the AppOps project. This server is completely separate from the Supabase application database.

> [!IMPORTANT]
> Complete this setup **before** running any `pulumi` commands in `infra/appops/`. AppOps must never manage the database that stores its own state.

## Architecture recap

| System | Responsibility |
|---|---|
| **State server** (this guide) | Pulumi state, deployment history, encrypted config and secrets |
| **Supabase** | Application schemas, roles, grants, and application data |
| **Cloudflare Secrets Store** | Runtime database credentials consumed by applications |

---

## Step 1 — Provision a PostgreSQL server

Any PostgreSQL 14+ server will work. Options:

| Option | Notes |
|---|---|
| Self-hosted (local or VPS) | Full control; you manage backups and TLS |
| Managed service (Neon, Supabase secondary project, Railway, etc.) | Simpler ops; usually includes automated backups and TLS |

**Requirements:**
- PostgreSQL 14 or newer
- TLS-encrypted connections (`sslmode=require` or stronger)
- Network-accessible from your development machine and CI runners
- Not the same server or project as the Supabase application database

---

## Step 2 — Create the state database and role

Connect to the state server as a superuser and run:

```sql
-- Create a dedicated database for Pulumi state
CREATE DATABASE pulumi_state;

-- Create a dedicated login role
CREATE ROLE pulumi_backend WITH LOGIN PASSWORD '<choose-a-strong-password>';

-- Grant full privileges on the state database
GRANT ALL PRIVILEGES ON DATABASE pulumi_state TO pulumi_backend;

-- Connect to the new database and grant schema privileges
\c pulumi_state
GRANT ALL ON SCHEMA public TO pulumi_backend;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT ALL ON TABLES TO pulumi_backend;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT ALL ON SEQUENCES TO pulumi_backend;
```

> [!CAUTION]
> Store the `pulumi_backend` password securely (password manager, not in a file in this repo). If you lose it, you can reset it on the server, but you need it for every `pulumi` operation.

---

## Step 3 — Configure network access

Ensure the state server accepts connections from:

1. **Your development machine** — for local `pulumi preview` and `pulumi up`.
2. **GitHub Actions runners** — if CI will run AppOps deployments in the future.

For managed services, this is usually an IP allowlist or VPC peering setting. For self-hosted, configure `pg_hba.conf` and firewall rules.

---

## Step 4 — Verify the connection

```bash
psql "postgres://pulumi_backend:<password>@<host>:<port>/pulumi_state?sslmode=require"
```

You should get a `pulumi_state=>` prompt. Verify you can create and drop a table:

```sql
CREATE TABLE _test (id int);
DROP TABLE _test;
```

---

## Step 5 — Log in to the Pulumi backend

Set environment variables for the state server connection. These will be needed every time you run Pulumi commands.

```bash
# Option A: Individual PG variables
export PGHOST=<state-server-host>
export PGPORT=<port>                    # typically 5432
export PGUSER=pulumi_backend
export PGPASSWORD='<password>'
export PGDATABASE=pulumi_state
export PGSSLMODE=require

pulumi login postgres://

# Option B: Full connection URI
pulumi login "postgres://pulumi_backend:<password>@<host>:<port>/pulumi_state?sslmode=require"
```

Pulumi will confirm: `Logged in to backend`.

---

## Step 6 — Configure the secrets provider

Pulumi DIY backends do not automatically encrypt secrets. You must configure a passphrase-based secrets provider.

```bash
# Set the passphrase (used to encrypt/decrypt config secrets in state)
export PULUMI_CONFIG_PASSPHRASE='<choose-a-separate-strong-passphrase>'
```

> [!CAUTION]
> **The passphrase is NOT the same as the database password.** It encrypts sensitive values inside Pulumi state. If you lose this passphrase, you cannot decrypt any secrets stored in existing stacks.
>
> - Never commit the passphrase.
> - Never store it in the state database.
> - Keep an independent secure backup (password manager).

When you later run `pulumi stack init dev` inside `infra/appops/`, Pulumi will use this passphrase to initialize the secrets provider for the stack.

---

## Step 7 — Set up backups

State loss means losing track of what AppOps has provisioned. Set up regular backups:

### For managed services
Most managed PostgreSQL services include automated daily backups. Verify this is enabled and confirm the retention period.

### For self-hosted

```bash
# Daily logical backup via cron
pg_dump "postgres://pulumi_backend:<password>@<host>:<port>/pulumi_state?sslmode=require" \
    --format=custom \
    --file="/backups/pulumi_state_$(date +%Y%m%d_%H%M%S).dump"
```

### Test a restore

After your first `pulumi up` deployment, test restoring from backup:

```bash
# Create a test database
createdb pulumi_state_test

# Restore into it
pg_restore --dbname=pulumi_state_test /backups/pulumi_state_<timestamp>.dump

# Verify by logging in to the test database
PGDATABASE=pulumi_state_test pulumi stack ls
```

---

## Step 8 — Document your settings

Record the following (values, not passwords) for use during implementation:

| Setting | Value | Where it goes |
|---|---|---|
| State server host | `<host>` | `PGHOST` or `pulumi login` URI |
| State server port | `<port>` | `PGPORT` or `pulumi login` URI |
| State database name | `pulumi_state` | `PGDATABASE` or `pulumi login` URI |
| State backend user | `pulumi_backend` | `PGUSER` or `pulumi login` URI |
| State backend password | *(in password manager)* | `PGPASSWORD` (never committed) |
| Config passphrase | *(in password manager)* | `PULUMI_CONFIG_PASSPHRASE` (never committed) |
| SSL mode | `require` | `PGSSLMODE` or query param |

---

## Concurrency and locking

The Pulumi PostgreSQL backend uses database-level advisory locks to prevent concurrent deployments from corrupting state. This means:

- Two `pulumi up` runs against the same stack will serialize automatically.
- If a deployment is interrupted (Ctrl-C, network failure), the lock may need to be manually released. Use `pulumi cancel` to clear a stuck lock.

---

## Recovery procedures

### Interrupted deployment
```bash
pulumi cancel          # clears the in-progress operation lock
pulumi refresh         # re-reads actual infrastructure state
pulumi preview         # verify the plan looks correct
```

### Corrupted or lost state
```bash
# Restore from backup
pg_restore --clean --dbname=pulumi_state /backups/pulumi_state_<timestamp>.dump

# Verify
pulumi stack ls
pulumi preview         # should show no changes if infra is intact
```

### Migrating to another backend
```bash
# Export state from current backend
pulumi stack export --file state.json

# Log in to new backend
pulumi login <new-backend-url>

# Import state
pulumi stack import --file state.json
```

---

## Verification checklist

Before handing off to implementation, confirm:

- [ ] PostgreSQL state server is running and accessible
- [ ] `pulumi_state` database exists
- [ ] `pulumi_backend` role can connect and create tables
- [ ] TLS is required (`sslmode=require` works)
- [ ] `pulumi login postgres://` succeeds
- [ ] `PULUMI_CONFIG_PASSPHRASE` is set and securely stored
- [ ] Backup procedure is documented or automated
- [ ] State server host/port are noted for Pulumi config

Once these are confirmed, implementation can proceed with Milestone 1.
