# AppOps API credentials: implementation plan

Status: **draft, nothing implemented, nothing verified against live Supabase.** Every behavior below that depends on Supabase, PostgREST, Cloudflare or a provider is marked **Untested** until someone runs it. Follow the rules in [`../../AGENTS.md`](../../AGENTS.md): no `pulumi` or `run.sh` command is run as part of writing code, only the Go checks.

## 1. Intent

Today AppOps gives each opted-in app a PostgreSQL schema, a login role with a generated password, and grants, then publishes a connection URI to Cloudflare Secrets Store. An app that talks to Supabase through the REST API (PostgREST) cannot use that credential, so it falls back to the single project-wide `SUPABASE_SERVICE_ROLE_KEY`, which is set by hand with `wrangler secret put` and is not scoped to the app.

Goal: an app can opt in, in its own `appops.yaml`, to an **API credential** that AppOps generates and publishes to Secrets Store, with no manual secret handling. The credential authenticates REST requests **as the app's own PostgreSQL role**.

### The one deliberate difference from the service-role key

The request was a credential "with the same abilities as `SUPABASE_SERVICE_ROLE_KEY`, scoped per app". Those two properties pull against each other, so this plan resolves it as follows. **Decision to confirm (D1).**

- Same **usage**: a bearer token sent to the Supabase REST API, read by the worker from Secrets Store.
- Different **power**: the token runs as `app_<name>_<env>`, so it can do exactly what that role's grants allow (the `permissions` already in `appops.yaml`), in the app's schema only. It does **not** bypass RLS and cannot reach other schemas. That is the point of per-app scoping.

If an app genuinely needs `service_role` power, it should keep using the shared key, or a Supabase secret key created by hand in the dashboard. AppOps will not mint `service_role` tokens.

## 2. Goals, non-goals, anti-goals

Goals

- `api.enabled: true` in `appops.yaml` makes AppOps mint a signed JWT whose `role` claim is the app's PostgreSQL role, and publish it to Secrets Store as `APP_<NAME>_<ENV>_API_TOKEN`.
- AppOps grants the app's role to `authenticator` so PostgREST can switch to it.
- Apps without `api` behave exactly as they do today (no new resources, no new required configuration).
- Rotation stays explicit and visible in git (`credentials.version`), plus a visible expiry.

Non-goals

- Changing the metrics-worker or any app code. That is a separate follow-up (section 9).
- Managing which schemas PostgREST exposes, Supabase Auth, RLS policies, or the Supabase dashboard (including importing the signing key).
- Automatic re-minting on a schedule. AppOps is applied by hand; section 7 covers the consequence.
- A `service_role`-equivalent token.

Anti-goals (this is a failure even if it technically works)

- The signing private key ending up readable by an app, in git, in logs, or in plain Pulumi config.
- Tokens that change on every `up` (constant churn of the Cloudflare secret).
- Any change to existing resource names or the database credential flow.

## 3. How it works

```text
appops.yaml (api.enabled)
        |
        v
AppOps  --signs-->  JWT { role: app_x_prod, iat, exp }  --publishes-->  Secrets Store
   |                                                                        |
   | GRANT app_x_prod TO authenticator                                       v
   v                                                        worker: Authorization: Bearer <JWT>
Supabase Postgres  <--SET LOCAL ROLE app_x_prod--  PostgREST  <-- apikey: <publishable key>
```

Facts this relies on (from the vendor docs; **Untested** here):

- PostgREST reads the JWT `role` claim and runs `SET LOCAL ROLE`; the role must be granted to `authenticator` (`GRANT <role> TO authenticator`).
- Supabase accepts tokens signed with an **imported asymmetric signing key**. Header needs `alg` and `kid` matching the imported key; payload needs `role` (any existing Postgres role) and a future `exp`. The request still needs an `apikey` header with a publishable key; the minted JWT goes only in `Authorization`.
- The legacy JWT secret is "no longer recommended" and is not used here.
- Supabase marks EdDSA as "coming soon"; supported choices are ES256, RS256 and HS256.

### Why RS256 (decision D2)

Pulumi recomputes the token on every run. ECDSA (ES256) signatures in Go are randomized, so the token, and therefore the Cloudflare secret input, would differ on every `up`. RSA PKCS#1 v1.5 (RS256) signatures are deterministic: same key, header and claims give the same token, so a run with no changes shows no diff. Supabase supports RS256 but recommends ES256. This plan uses **RS256 (RSA 2048)** and requires a unit test proving determinism. If you prefer ES256, the implementation needs a deterministic ECDSA (RFC 6979) signer or a different approach to stability; that is more code and a new dependency.

## 4. Configuration

### `appops.yaml`

```yaml
api:
  enabled: true        # default false; omit the block to opt out
  expires_in_days: 180 # default 180; allowed 7..365
```

- Rotation reuses `credentials.version`: bumping it re-mints the token (and regenerates the database password, as today). Keep one rotation knob; do not add `api.version`.
- Validation (add to `internal/config`, with tests): `expires_in_days` integer in range; `api` accepted for any app; enabling it requires the project-level settings below at program start.
- Update `docs/OPERATIONS.md` for the new option, as `AGENTS.md` requires.

### Project-level settings (Pulumi config / environment)

| Setting | Source | Secret? | Meaning |
| --- | --- | --- | --- |
| `SUPABASE_JWT_SIGNING_KEY` | environment (`.env`, gitignored) or Pulumi secret config | **Yes** | RSA 2048 private key, PKCS#8 PEM, the one imported into Supabase |
| `appops:supabaseJwtKid` / `SUPABASE_JWT_KID` | Pulumi config or environment | No | `kid` of that key as shown in Supabase |

Both are required only when at least one discovered app has `api.enabled`. If any such app exists and either is missing, the program fails before creating resources (fail closed, clear message). Add placeholders to `.env.example`; never real values. Do not log either value.

The publishable key is public by design. AppOps does **not** publish it; apps take it as an ordinary Worker variable alongside the project URL.

## 5. Changes by file

New and changed code, in dependency order. Use the standard library for signing (no JWT package needed) to avoid a new dependency and license review.

1. `internal/config/config.go`
   - Add `APIConfig { Enabled bool; ExpiresInDays int }` and `API APIConfig` on `AppConfig` (yaml `api`).
   - Defaults and validation in `Validate()`.
   - Add `func (c *AppConfig) APITokenSecretName(env string) string` returning `APP_<NAME>_<ENV>_API_TOKEN`, built the same way as `SecretName`. Check the combined name length against the Secrets Store limit (**unknown; verify**).
2. `internal/apijwt/apijwt.go` (new package)
   - `ParseRSAPrivateKey(pem string) (*rsa.PrivateKey, error)`.
   - `Mint(key *rsa.PrivateKey, kid, role string, iat, exp time.Time) (string, error)` producing `base64url(header).base64url(payload).base64url(sig)` with header `{"alg":"RS256","kid":<kid>,"typ":"JWT"}` and payload `{"role":<role>,"iat":..,"exp":..,"iss":"appops"}`, signed with `rsa.SignPKCS1v15` over SHA-256. Marshal with a fixed field order (use a struct, not a map) so output is stable.
   - Start with exactly those claims. If Supabase or PostgREST rejects the token for a missing `aud` or similar, add the claim after the live test and record it here.
3. `internal/postgres/postgres.go`
   - When `app.API.Enabled`, add `postgresql.NewGrantRole` named `<app>-<env>-api-authenticator` that makes `authenticator` a member of the app role. Verify the argument names in the provider SDK source before using them: in `pulumi-postgresql` v3.18.3 `GrantRole` takes the recipient `Role` and the granted `GrantRole`, so the recipient is `authenticator` and the granted role is the app role.
   - Depend on `role`. Do not manage `authenticator` itself.
   - Return the grant in `AppResources` so main can order the token publication after it.
4. `internal/secrets/secrets.go`
   - Add `PublishAPIToken(ctx, app, env, token pulumi.StringOutput, store, deps...)` mirroring `PublishDatabaseURL`: resource name `<app>-<env>-api-token`, secret name from `APITokenSecretName`, scope `workers`, comment `Managed by AppOps`.
5. `main.go`
   - Read the project-level settings once, only if any app has `api.enabled`.
   - Add a time resource for stable `iat`/`exp`: use `github.com/pulumiverse/pulumi-time` (`time.Static` triggered by `credentials.version`, or `time.Rotating`, see D3). **Verify the module path, resource API and license before adding it to `go.mod`.** The reason: calling `time.Now()` inside the program would change the token every run.
   - For each app with `api.enabled`: derive the token with `ApplyT` from the time resource outputs, then call `PublishAPIToken` with dependencies on the role, schema grants and the authenticator grant.
   - Export `apiTokenExpiresAt-<app>` so the expiry is visible in `pulumi stack output` (outputs only, no secrets).
6. `.env.example`, `docs/OPERATIONS.md`, `README.md`, `AGENTS.md` (conventions: add the secret naming rule)
   - Document setup, rotation, kill switch and limitations (sections 6 to 8). Mark each procedure verified, partly verified or untested, honestly.

### Tests (Pulumi mocks and plain unit tests; no database or network)

- `config`: `api` defaults, range checks, secret-name derivation and the length limit.
- `apijwt`: header and claims are as specified; the signature verifies with the public key; **identical inputs give identical output**; different role or `exp` gives a different token; a malformed key returns an error.
- `postgres`: the grant exists only when `api.enabled`; apps without `api` produce exactly the resources they do today (regression guard on resource names, which are part of state).
- `secrets`: the token secret has the expected name, scope and dependencies.
- Run `gofmt -l .`, `go vet ./...`, `go test ./...` from `infra/appops`. CI does not run these.

## 6. Operations (all **Untested** until run)

One-time project setup (by you, in the Supabase dashboard; not done by AppOps):

1. Generate an RSA 2048 key pair locally (for example with `openssl`). Keep the private key out of the repository.
2. Import it as a standby signing key in Supabase and note its `kid`.
3. Activate it with **Rotate key**. Check the effect first: the active signing key also signs Supabase Auth tokens. If the project has Auth users, plan for that. The legacy key stays valid until you revoke it.
4. Put the private key and `kid` in the AppOps environment.

Enabling an app: add the `api` block, `./run.sh preview`, review that only the expected resources appear (authenticator grant, time resource, one secret), then `up`. The project's REST schema exposure (hosted dashboard setting and `db/supabase/config.toml`) is still a manual prerequisite.

Rotating a token: bump `credentials.version`, preview, `up`. The worker reads the secret at request time, so no worker redeploy is needed (per `OPERATIONS.md`, **Untested**).

Kill switch for one app: set `api.enabled: false` (removes the membership and the secret) or run `REVOKE <app role> FROM authenticator` by hand for an immediate stop. Revoking the signing key in Supabase invalidates every app's token at once.

## 7. Risks and what to do about them

| Risk | Consequence | Mitigation |
| --- | --- | --- |
| The signing key can mint a token for **any** role, including `service_role` | Whoever holds it owns the project's API. AppOps (and the operator's machine) becomes the most sensitive component | Keep it out of git and logs, load it only when an app opts in, treat the AppOps environment as production-secret, document it |
| Expiry with manual applies | If nobody runs `up` before `exp`, the app's REST calls fail with an auth error | Default 180 days; export the expiry; document a calendar reminder. A scheduled job is a possible follow-up, not in scope |
| Hosted Supabase may restrict `GRANT <role> TO authenticator` | The whole approach fails for that app | Phase 0 below tests it before any code |
| PostgREST may require extra claims (`aud`) | Requests rejected | Test in Phase 0 and add the claim to `apijwt` |
| The role needs `EXECUTE` on functions and the schema must be exposed | RPCs fail with permission or schema errors | Check in Phase 0 whether default `EXECUTE` is enough. If not, add a `permissions.functions` option with validation, a test and docs, as a separate small change |
| `Mint` output is not stable | Cloudflare secret changes every run | Determinism test (D2) |
| Removing `api.enabled` later | Drops the grant and the secret | Intended; the role and schema stay protected as today |

## 8. Build order

Phase 0, a manual spike before any code (you run it; it touches live Supabase, so it needs your say-so): with a throwaway role and key, confirm (a) `GRANT <role> TO authenticator` works on the hosted project, (b) a token signed with an imported RS256 key and `role: <role>` is accepted by the REST API alongside a publishable `apikey`, (c) which claims are required, (d) RPC execute and schema exposure behavior, (e) the effect of rotating the signing key on Supabase Auth. Write the results into this document. If (a) or (b) fails, stop and rethink.

Phase 1: config option, validation and tests. Phase 2: `apijwt` package and tests. Phase 3: authenticator grant and tests. Phase 4: token publication and tests. Phase 5: `main.go` wiring, time resource, exports. Phase 6: documentation. Each phase leaves the existing flow unchanged for apps without `api`. Phase 7, live verification: preview shows only the expected additions for one app; `up`; the secret exists; a real REST call succeeds as the app's role; a call to another schema fails.

## 9. Follow-ups outside this plan

- metrics-worker cutover: add `api` to its `appops.yaml`, add a `[[secrets_store_secrets]]` binding, read the token with `await env.API_TOKEN.get()`, send it as `Authorization: Bearer` with the publishable key as `apikey`, then delete the `SUPABASE_SERVICE_ROLE_KEY` Wrangler secret. The Secrets Store binding is **Untested** and not available under `wrangler dev`.
- URL shortener: separately decide REST (this feature) or direct Postgres through Hyperdrive. The Hyperdrive experiment is parked on `wip/metrics-worker-hyperdrive`.

## 10. Decisions to confirm

- **D1** Scoped role token, not `service_role` power (section 1).
- **D2** RS256 for deterministic tokens (section 3), accepting Supabase's ES256 recommendation as a trade-off.
- **D3** Token validity: `time.Static` keyed to `credentials.version` plus an expiry export (simplest), or `time.Rotating` so the next `up` after a rotation date re-mints automatically. Default in this plan: the simpler `time.Static`; `time.Rotating` is an optional upgrade once Phase 0 succeeds.
- **D4** Default expiry 180 days, range 7 to 365.
