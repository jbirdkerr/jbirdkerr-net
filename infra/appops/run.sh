#!/usr/bin/env bash
# infra/appops/run.sh
# Wrapper to automatically load .env before running pulumi commands

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

if [ -f .env ]; then
  set -a
  # shellcheck source=/dev/null
  source .env
  set +a
fi

# Construct PULUMI_BACKEND_URL strictly from PULUMI_STATE_PG* variables if not directly set
if [ -z "${PULUMI_BACKEND_URL:-}" ]; then
  STATE_USER="${PULUMI_STATE_PGUSER:-}"
  STATE_PASS="${PULUMI_STATE_PGPASSWORD:-}"
  STATE_HOST="${PULUMI_STATE_PGHOST:-}"
  STATE_PORT="${PULUMI_STATE_PGPORT:-5432}"
  STATE_DB="${PULUMI_STATE_PGDATABASE:-postgres}"
  STATE_SSL="${PULUMI_STATE_PGSSLMODE:-require}"

  # Fallback to standard PG* only if PULUMI_STATE_PG* not set at all
  if [ -z "$STATE_HOST" ]; then
    STATE_USER="${PGUSER:-}"
    STATE_PASS="${PGPASSWORD:-}"
    STATE_HOST="${PGHOST:-}"
    STATE_PORT="${PGPORT:-5432}"
    STATE_DB="${PGDATABASE:-postgres}"
    STATE_SSL="${PGSSLMODE:-require}"
  fi

  if [ -n "$STATE_USER" ] && [ -n "$STATE_PASS" ] && [ -n "$STATE_HOST" ]; then
    PULUMI_BACKEND_URL="postgres://${STATE_USER}:${STATE_PASS}@${STATE_HOST}:${STATE_PORT}/${STATE_DB}?sslmode=${STATE_SSL}"
  fi
fi

if [ -n "${PULUMI_BACKEND_URL:-}" ]; then
  export PULUMI_BACKEND_URL
fi

# If "login" is called without arguments, default to $PULUMI_BACKEND_URL
if [ "${1:-}" = "login" ] && [ "$#" -eq 1 ]; then
  if [ -n "${PULUMI_BACKEND_URL:-}" ]; then
    echo "Logging into Pulumi state backend..."
    # PULUMI_BACKEND_URL is exported above; passing it as an argument would expose the password in `ps`.
    exec pulumi login
  else
    echo "Error: PULUMI_BACKEND_URL is not set and could not be composed from PGUSER, PGPASSWORD, PGHOST." >&2
    exit 1
  fi
fi

exec pulumi "$@"
