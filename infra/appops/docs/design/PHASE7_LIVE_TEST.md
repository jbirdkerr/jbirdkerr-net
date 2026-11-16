# Phase 7 live test: AppOps API credentials

Status: **not run.** This is the checklist for the first live run of the feature in [API_CREDENTIALS_PLAN.md](./API_CREDENTIALS_PLAN.md). Every step touches real infrastructure and is run by the owner. Record results in plan section 8 and update the status lines in [`../OPERATIONS.md`](../OPERATIONS.md) §3; mark a step verified only if it was run.

## What "pass" means

- A token signed with a key **imported through the Supabase dashboard** is accepted by the **hosted** REST API, as the app's role.
- `./run.sh preview` for one app shows only the four expected creates, and a second preview after `up` shows no changes.
- The app's role can call its RPCs, cannot reach other schemas, and loses access when the kill switch is used.

## Before you start

- Branch `feat/appops-api-credentials` checked out; from `infra/appops`: `gofmt -l .`, `go vet ./...`, `go test ./...` all clean.
- `./run.sh preview` on the branch, **before any `appops.yaml` change**, shows no changes. If it does, stop: something else has drifted.
- Optional but recommended: run the read-only check from plan section 9 (`proacl` on `metrics_worker` functions) on production and note the result. It decides whether the anon `EXECUTE` fix should land first.

### A throwaway token minter

Tokens are deterministic, so a token minted locally with the same key, role, `issued_on` and `expires_in_days` is byte-for-byte the one AppOps publishes. That avoids reading the secret back out of Pulumi state or Cloudflare. Create `infra/appops/tmp-phase7/main.go` (do **not** commit it; delete it at the end):

```go
package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jbirdkerr/jbirdkerr-net/infra/appops/internal/apijwt"
)

func main() {
	key, kid, err := apijwt.ParseSigningJWK(os.Getenv("SUPABASE_JWT_SIGNING_JWK"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	iat, _ := time.Parse(time.DateOnly, os.Args[2])
	days, _ := strconv.Atoi(os.Args[3])
	tok, err := apijwt.Mint(key, kid, os.Args[1], iat, iat.AddDate(0, 0, days))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(tok)
}
```

Use it as `TOKEN=$(go run ./tmp-phase7 <role> <issued_on> <days>)` with the key in the environment. Keep the token in a shell variable; do not echo it or paste it anywhere.

## Part A: hosted spike on a throwaway project

Answers the open Phase 0 checks (b), (e) and (f) without touching production. Takes about 15 minutes.

1. Create a new free Supabase project. Note its URL and publishable key.
2. `supabase gen signing-key --algorithm ES256 > spike-key.json` outside the repository. In the dashboard, import it as a **standby** key with the same `kid`, then **Rotate key** to make it current.
3. In the SQL editor: `create role spike_app nologin; grant spike_app to authenticator; grant usage on schema public to spike_app;` and a probe: `create function public.whoami() returns text language sql as $$ select current_user::text $$; grant execute on function public.whoami() to spike_app;`
4. With `SUPABASE_JWT_SIGNING_JWK` set to the spike key (one line), mint: `TOKEN=$(go run ./tmp-phase7 spike_app $(date -u +%F) 7)`.
5. Calls, each against `<url>/rest/v1/rpc/whoami` with `-X POST -H 'Content-Type: application/json' -d '{}'`:

   | Request | Expect |
   | --- | --- |
   | `apikey: <publishable>` + `Authorization: Bearer $TOKEN` | `200 "spike_app"` |
   | same, **no** `apikey` header | rejected (`401`) |
   | publishable key only | `"anon"` or permission error, not `spike_app` |
   | token minted with an issued_on 8 days ago and 7 days validity (expired) | `401` |

6. (f): in the dashboard, create a new Supabase-managed standby key and **Rotate key** to it. Repeat the first call: expect still `200 "spike_app"` (the imported key is now "previously used").
7. (e): note anything the rotations changed for Auth or the legacy keys.
8. Delete the spike project and `spike-key.json`.

**Stop if step 5 row 1 or step 6 fails.** Row 1 failing means the approach does not work on hosted Supabase; step 6 failing means D5's plan before enabling Auth needs rethinking (production can still proceed with the key left current).

## Part B: production key setup (plan section 6)

1. Generate the production key the same way, outside the repository.
2. Import it as a standby key in the production project, **Rotate key** to it. Do **not** revoke the legacy JWT secret: the metrics-worker's `SUPABASE_SERVICE_ROLE_KEY` may depend on it.
3. Put it in `infra/appops/.env` as one line: `SUPABASE_JWT_SIGNING_JWK='{...}'`.
4. Smoke test the live site: the metrics-worker still records events (it uses the shared key, which should be unaffected).

## Part C: enable one app (metrics-worker, `dev`)

The worker does not use the token yet, so enabling it changes nothing for live traffic.

1. **Before `up`, prove the token is accepted.** Mint `TOKEN=$(go run ./tmp-phase7 app_metrics_worker_dev $(date -u +%F) 180)` and call `POST /rest/v1/rpc/record_worker_health` with `Content-Profile: metrics_worker`. Expect `403 permission denied to set role "app_metrics_worker_dev"`: the gateway accepted the signature and PostgREST tried the role, which is not yet granted to `authenticator`.
2. Edit `apps/metrics-worker/appops.yaml`:

   ```yaml
   permissions:
     # existing tables and sequences entries unchanged
     functions:
       - EXECUTE
   api:
     enabled: true
     issued_on: <today, YYYY-MM-DD, UTC>
   ```

3. `go test ./...`, then `./run.sh preview`. Expect **exactly four creates and nothing else**:
   - `metrics-worker-dev-function-grants`
   - `metrics-worker-dev-default-function-privs`
   - `metrics-worker-dev-api-authenticator`
   - `metrics-worker-dev-api-token` (value shown as `[secret]`)

   Any update, replace or delete of an existing resource: stop.
4. `./run.sh up`, then `./run.sh preview` again: expect **no changes** (token determinism).
5. `./run.sh stack output`: `apiTokenExpiresAt-metrics-worker` is `issued_on` + 180 days.
6. Cloudflare dashboard: `APP_METRICS_WORKER_DEV_API_TOKEN` exists with `workers` scope. Do not try to read its value.
7. SQL editor (read-only):

   ```sql
   select m.rolname as member, am.admin_option, am.inherit_option, am.set_option
   from pg_auth_members am
   join pg_roles r on r.oid = am.roleid join pg_roles m on m.oid = am.member
   where r.rolname = 'app_metrics_worker_dev';

   select p.oid::regprocedure as fn,
          has_function_privilege('app_metrics_worker_dev', p.oid, 'EXECUTE') as can_execute
   from pg_proc p where p.pronamespace = 'metrics_worker'::regnamespace;
   ```

   Expect an `authenticator` row with `admin_option` false and `set_option` true, and `can_execute` true for **every** function. That last check settles the open question of whether the provider's empty `objects` list covers all functions.

## Part D: REST checks as the app role

Mint the token again with the same `issued_on` (it equals the published one). Headers for all calls: `apikey: <publishable>`, `Authorization: Bearer $TOKEN`, `Content-Type: application/json`.

| Request | Expect |
| --- | --- |
| `POST /rest/v1/rpc/record_worker_health`, `Content-Profile: metrics_worker`, body `{"p_request_type":"phase7-test","p_status_code":200}` | `204` (adds 1 to `total_requests` in the single `worker_health_stats` row; an unknown request type touches no other counter) |
| `GET /rest/v1/site_metrics?limit=1`, `Accept-Profile: metrics_worker` | `200 []` (RLS has no policy for the role; expected, see plan section 9) |
| `GET /rest/v1/<any table>`, `Accept-Profile: public` | permission error |
| the RPC call without `apikey` | `401` |

## Part E: kill switch and cleanup

1. `REVOKE app_metrics_worker_dev FROM authenticator;` in the SQL editor, repeat the RPC call: expect `403 permission denied to set role`.
2. `./run.sh preview`: expect AppOps to propose re-creating the membership (drift detected). `./run.sh up` to restore it, or set `api.enabled: false` if the test should end with the feature off.
3. Undo the Part D call, if you care about the exact count: `update metrics_worker.worker_health_stats set total_requests = total_requests - <successful RPC calls> where id;` (calls that returned `403` changed nothing).
4. Delete `infra/appops/tmp-phase7/` and unset `TOKEN`.
5. Record results in plan section 8 and the `OPERATIONS.md` §3 status. Decide whether to keep `api` enabled for metrics-worker until its cutover (plan section 9).

## Rollback at any point

Set `api.enabled: false` and remove `permissions.functions` in `apps/metrics-worker/appops.yaml`, then `preview` (expect four deletes) and `up`. Schemas, roles and the database credential are untouched by this feature. The imported signing key can stay current in Supabase; it is unused without AppOps tokens.
