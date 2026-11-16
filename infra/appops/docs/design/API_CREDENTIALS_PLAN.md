# AppOps API credentials: implementation plan

Status: **Phases 1 to 6 implemented** (config, `apijwt`, grants, token publication, `main.go` wiring, docs; unit tests with Pulumi mocks). **Nothing verified against live Supabase or Cloudflare yet:** Phase 7 is the checklist in [PHASE7_LIVE_TEST.md](./PHASE7_LIVE_TEST.md). Every behavior below that depends on Supabase, PostgREST, Cloudflare or a provider is marked **Untested** until someone runs it. Follow the rules in [`../../AGENTS.md`](../../AGENTS.md): no `pulumi` or `run.sh` command is run as part of writing code, only the Go checks.

Phase 0 ran on 2026-11-13 against an **isolated local Supabase stack**, not the hosted project; results and what still needs a hosted check are in section 8.

Revised 2026-11-13 after a review against `infra/appops/`, `db/supabase/migrations/` and the Supabase signing-keys docs. Changes: ES256 instead of RS256, the signing key is stored as a JWK whose `kid` you choose, a function `EXECUTE` grant is now required (metrics-worker cannot work without it), the token is marked secret explicitly, the token gets its own rotation trigger, and Phase 0 moves off the production project.

## 1. Intent

Today AppOps gives each opted-in app a PostgreSQL schema, a login role with a generated password, and grants, then publishes a connection URI to Cloudflare Secrets Store. An app that talks to Supabase through the REST API (PostgREST) cannot use that credential, so it falls back to the single project-wide `SUPABASE_SERVICE_ROLE_KEY`, which is set by hand with `wrangler secret put` and is not scoped to the app.

Goal: an app can opt in, in its own `appops.yaml`, to an **API credential** that AppOps generates and publishes to Secrets Store, with no manual secret handling. The credential authenticates REST requests **as the app's own PostgreSQL role**.

### The one deliberate difference from the service-role key

The request was a credential "with the same abilities as `SUPABASE_SERVICE_ROLE_KEY`, scoped per app". Those two properties pull against each other, so this plan resolves it as follows. **Decision to confirm (D1).**

- Same **usage**: a bearer token sent to the Supabase REST API, read by the worker from Secrets Store.
- Different **power**: the token runs as `app_<name>_<env>`, so it can do exactly what that role's grants allow (the `permissions` in `appops.yaml`), in the app's schema only. It does **not** bypass RLS and cannot reach other schemas. That is the point of per-app scoping.

If an app genuinely needs `service_role` power, it should keep using the shared key, or a Supabase secret key created by hand in the dashboard. AppOps will not mint `service_role` tokens.

### Consequence for existing apps: RPCs need an explicit grant

The current migrations make the scoped role unusable for RPCs as they stand:

- `metrics_worker.record_site_events` and `metrics_worker.record_worker_health` run `REVOKE ALL ... FROM PUBLIC` and grant `EXECUTE` only to `service_role` and `postgres`. The worker writes through `/rest/v1/rpc/...`, so a role token fails on its first write.
- The `shortener` RPCs follow the same pattern.
- Tables in these schemas have RLS enabled. The "public can read" `SELECT` policies apply only to `anon` and `authenticated`, and the app role does not bypass RLS. **Reads as the app role return an empty result, not an error** (confirmed on the local stack: `200 []` on tables that held rows). Direct table writes are blocked. The functions are `SECURITY DEFINER`, so `EXECUTE` is the only grant the RPC path needs (confirmed locally: `403 permission denied for function` before the grant, success after).

So function `EXECUTE` is part of this plan (`permissions.functions`, section 4), not a follow-up. D1 holds only once that grant exists. Apps that read tables over REST also need RLS policies that cover their role; that is a migration concern outside AppOps (section 9).

## 2. Goals, non-goals, anti-goals

Goals

- `api.enabled: true` in `appops.yaml` makes AppOps mint a signed ES256 JWT whose `role` claim is the app's PostgreSQL role, and publish it to Secrets Store as `APP_<NAME>_<ENV>_API_TOKEN`.
- AppOps grants the app's role to `authenticator` so PostgREST can switch to it.
- `permissions.functions: [EXECUTE]` grants `EXECUTE` on the app schema's functions (existing and future), so RPCs work as the app role.
- Apps without `api` or `permissions.functions` behave exactly as they do today (no new resources, no new required configuration).
- Rotation stays explicit and visible in git, plus a visible expiry.

Non-goals

- Changing the metrics-worker or any app code. That is a separate follow-up (section 9).
- Managing which schemas PostgREST exposes, Supabase Auth, RLS policies, or the Supabase dashboard (including importing the signing key).
- Automatic re-minting on a schedule. AppOps is applied by hand; section 7 covers the consequence.
- A `service_role`-equivalent token.

Anti-goals (this is a failure even if it technically works)

- The signing private key ending up readable by an app, in git, in logs, or in plain Pulumi config.
- The minted token appearing unencrypted in Pulumi state or in preview output.
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

Facts this relies on. Sources: PostgREST docs and the Supabase [JWT signing keys](https://supabase.com/docs/guides/auth/signing-keys) page, read 2026-11-13. **Untested** here.

- PostgREST reads the JWT `role` claim and runs `SET LOCAL ROLE`; the role must be granted to `authenticator` (`GRANT <role> TO authenticator`). Supabase's own docs recommend this grant for custom roles.
- Supabase accepts tokens signed with an **imported private key**. The import is a **JWK** (`kty`, `crv`, `x`, `y`, `d`, `kid`), not a PEM. **You choose the `kid`**, and the JWT header must carry the same value.
- Required claims: `role` (an existing Postgres role) and a future `exp`. `sub` is optional. The minted JWT goes only in `Authorization: Bearer`; it cannot be used as `apikey`. The request still needs an `apikey` header with a publishable key.
- Key states: a **standby** key's public key is published, but signatures are accepted from the **current** key only. **Previously used** keys stay trusted until revoked. **Revoked** keys are not trusted. So the imported key must become current at least once (section 6).
- Algorithms: ES256 (P-256, recommended), RS256 (RSA 2048, "we recommend using the P-256 elliptic curve instead"), HS256 (not recommended), EdDSA ("coming soon").
- The legacy JWT secret is "no longer recommended" and is not used here.

### Why ES256 (decision D2)

Pulumi recomputes the token on every run, so the signature must be deterministic or the Cloudflare secret input changes on every `up`. On Go 1.26 (the `go.mod` toolchain), `ecdsa.PrivateKey.Sign(nil, digest, crypto.SHA256)` produces a **deterministic RFC 6979** signature (confirmed with `go doc crypto/ecdsa.PrivateKey.Sign`). Passing a reader instead gives a randomized signature, and `ecdsa.SignASN1` is always randomized, so do not use either.

This uses only the standard library. The one extra step: Go returns an ASN.1 DER signature, and JWS ES256 needs the raw 64-byte `r || s` form (each value left-padded to 32 bytes). Convert with `encoding/asn1`. A unit test must prove determinism and verify the raw signature with the public key.

ES256 is Supabase's recommendation, so there is no trade-off left to accept. RS256 (deterministic PKCS#1 v1.5) remains a fallback if ES256 fails in Phase 0.

## 4. Configuration

### `appops.yaml`

```yaml
api:
  enabled: true        # default false; omit the block to opt out
  expires_in_days: 180 # default 180; allowed 7..365
  issued_on: 2026-11-13 # required when enabled; change it to re-mint the token

permissions:
  functions:
    - EXECUTE          # optional; the only allowed value
```

- **Token rotation (D3, revised in Phase 5):** `iat` is midnight UTC on `api.issued_on` and `exp` is `iat + expires_in_days`. Changing `issued_on` (or `expires_in_days`) re-mints the token without rotating the database password. Bumping `credentials.version` rotates only the password; a full rotation changes both. This replaced the earlier `api.version` plus `time.Static` design (section 10, D3).
- **Validation** (add to `internal/config`, with tests): `expires_in_days` an integer in range; `api.issued_on` a `YYYY-MM-DD` date, required when `api.enabled`, at most one day ahead of today (UTC); `permissions.functions` only `EXECUTE`, case-insensitive like the other permissions; `api` accepted for any app; enabling it requires the project-level setting below at program start.
- `permissions.functions` is independent of `api`, because a direct database connection can call functions too. Without it, an `api` app with only `SECURITY DEFINER` RPCs is unusable, so `docs/OPERATIONS.md` must say so. A validation warning is optional; do not make it an error.
- Update `docs/OPERATIONS.md` for the new options, as `AGENTS.md` requires.

### Project-level settings (Pulumi config / environment)

| Setting | Source | Secret? | Meaning |
| --- | --- | --- | --- |
| `SUPABASE_JWT_SIGNING_JWK` | environment only (`.env`, gitignored), like the database password | **Yes** | The P-256 **private** JWK imported into Supabase, as single-line JSON. Must include `kid`; AppOps takes the `kid` from it, so there is no separate `kid` setting |

It is required only when at least one discovered app has `api.enabled`. If any such app exists and the key is missing or malformed, the program fails before creating resources (fail closed, clear message that does not include the key). Check at parse time that `kty` is `EC`, `crv` is `P-256`, `kid` is non-empty, and `x`/`y` match `d`. Add a placeholder to `.env.example`, never a real value. Do not log the key.

The publishable key is public by design. AppOps does **not** publish it; apps take it as an ordinary Worker variable alongside the project URL.

## 5. Changes by file

New and changed code, in dependency order. Use the standard library for JWK parsing and signing (no JWT or JOSE package) to avoid a new dependency and license review.

1. `internal/config/config.go`
   - Add `APIConfig { Enabled bool; ExpiresInDays int; IssuedOn string }` (plus `IssuedAt()` and `ExpiresAt()`) and `API APIConfig` on `AppConfig` (yaml `api`).
   - Add `Functions []string` to `PermissionsConfig` (yaml `functions`).
   - Defaults and validation in `Validate()`.
   - Add `func (c *AppConfig) APITokenSecretName(env string) string` returning `APP_<NAME>_<ENV>_API_TOKEN`, built the same way as `SecretName`. Check the combined name length against the Secrets Store limit (**unknown; verify**).
2. `internal/apijwt/apijwt.go` (new package)
   - `ParseSigningJWK(jwk string) (*ecdsa.PrivateKey, string, error)` returning the key and its `kid`. Reject anything that is not an EC P-256 private key with a `kid`. Build the key with `ecdsa.ParseRawPrivateKey(elliptic.P256(), d)` (Go 1.26 standard library; used successfully in Phase 0) and reject the JWK if its `x`/`y` do not match the derived public key.
   - `Mint(key *ecdsa.PrivateKey, kid, role string, iat, exp time.Time) (string, error)` producing `base64url(header).base64url(payload).base64url(sig)` with header `{"alg":"ES256","kid":<kid>,"typ":"JWT"}` and payload `{"role":<role>,"iat":..,"exp":..,"iss":"appops"}`. Sign the SHA-256 digest with `key.Sign(nil, digest, crypto.SHA256)` (deterministic, see D2), then convert ASN.1 to raw `r || s`. Marshal with a fixed field order (use a struct, not a map) so output is stable.
   - Start with exactly those claims. If Supabase or PostgREST rejects the token over `iss`, a missing `aud` or similar, change the claims after Phase 0 and record it here.
3. `internal/postgres/postgres.go`
   - When `app.API.Enabled`, add `postgresql.NewGrantRole` named `<app>-<env>-api-authenticator`. Checked in the `pulumi-postgresql` v3.18.3 SDK source: `Role` is "the role that is granted a new membership" (set it to `authenticator`) and `GrantRole` is "the role that is added" (set it to the app role). Leave `WithAdminOption` false. Depend on `role`. Do not manage `authenticator` itself.
   - When `permissions.functions` is set, add a `postgresql.Grant` (`<app>-<env>-function-grants`, `ObjectType` `function`, all functions in the schema) and a `postgresql.DefaultPrivileges` (`<app>-<env>-default-function-privs`, owner `migrationOwner`), mirroring the table and sequence grants. Verify that both resources accept `function` as the object type in v3.18.3 before using it.
   - Return the new resources in `AppResources` so main can order the token publication after them.
   - On PostgreSQL 17, `GRANT <app role> TO authenticator` needs the grantor to hold `ADMIN` on the app role. The role that created it has that implicitly, so this works when AppOps connects as the same role that created the app roles (`postgres` today). Phase 0 confirms it.
4. `internal/secrets/secrets.go`
   - Add `PublishAPIToken(ctx, app, env, token pulumi.StringOutput, store, deps...)` mirroring `PublishDatabaseURL`: resource name `<app>-<env>-api-token`, secret name from `APITokenSecretName`, scope `workers`, comment `Managed by AppOps`.
   - Wrap the value in `pulumi.ToSecret(...)`. The token is computed from non-secret time outputs and a key held in a Go variable, so its own Output is not secret. Checked during Phase 4: `pulumi-cloudflare` v6.23.3's `NewSecretsStoreSecret` already wraps `Value` with `pulumi.ToSecret` and lists `value` in `AdditionalSecretOutputs`, so the explicit wrap is a safeguard against a future SDK change, not the only protection. A mock test asserts the value sent is secret.
5. `main.go`
   - Read and parse the signing JWK once from `SUPABASE_JWT_SIGNING_JWK`, only if any app has `api.enabled`, before any resource is registered (`internal/apitoken.LoadSigningKey`).
   - For each app with `api.enabled` (`internal/apitoken.Provision`): mint the token from `api.issued_on` and `expires_in_days` (no clock involved, so it is deterministic), refuse to publish one that has already expired, warn within 30 days of expiry, wrap it with `pulumi.ToSecret`, then call `PublishAPIToken` with dependencies on the role, schema grant, function grant (if any) and the authenticator grant.
   - No time resource. Phase 5 checked `github.com/pulumiverse/pulumi-time/sdk`: latest release v0.1.0 (2024-09-04), Apache-2.0 (not MPL-2.0 as guessed), provider binary downloaded from GitHub at run time. A third-party, two-year-old provider process would inherit the AppOps environment, including the signing key, so it was replaced by `api.issued_on`.
   - Export `apiTokenExpiresAt-<app>` so the expiry is visible in `pulumi stack output` (outputs only, no secrets).
6. `.env.example`, `docs/OPERATIONS.md`, `README.md`, `AGENTS.md` (conventions: add the token secret naming rule)
   - Document setup, rotation, kill switch and limitations (sections 6 to 8). Mark each procedure verified, partly verified or untested, honestly.

### Tests (Pulumi mocks and plain unit tests; no database or network)

- `config`: `api` defaults (including `version`), range checks, `permissions.functions` validation, secret-name derivation and the length limit.
- `apijwt`: header and claims are as specified; the raw 64-byte signature verifies with the public key; **identical inputs give identical output**; different role or `exp` gives a different token; a malformed JWK, wrong curve, wrong key type or missing `kid` returns an error that does not contain key material.
- `postgres`: the authenticator grant exists only when `api.enabled`; the function grants exist only when `permissions.functions` is set; apps without either produce exactly the resources they do today (regression guard on resource names, which are part of state).
- `secrets`: the token secret has the expected name, scope and dependencies, and its value is secret. `secrets_test.go` has no Pulumi mocks today, so add the mock setup (as `internal/postgres` tests do).
- Run `gofmt -l .`, `go vet ./...`, `go test ./...` from `infra/appops`. CI does not run these.

## 6. Operations (all **Untested** until run)

One-time project setup (by you, in the Supabase dashboard; not done by AppOps). Do not do this on production until Phase 0 has passed on a separate project.

1. Generate a P-256 private key as a JWK: `supabase gen signing-key --algorithm ES256 > key.json` (the CLI describes the output as usable "to import in the dashboard"; it sets a UUID `kid`). It prints the private key to standard output, so redirect it to a file outside the repository and do not run it where output is logged. Used on the local stack in Phase 0; the dashboard import is **Untested**.
2. Import it as a standby key in Supabase with the same `kid`.
3. Make it current with **Rotate key**. This step is required: Supabase accepts signatures from the current key only, not from a standby key. While it is current, Supabase Auth signs new user tokens with it. Existing user tokens stay valid, and the legacy secret stays trusted until you revoke it.
4. Put the private JWK in the AppOps environment.

The imported key stays current while the project does not use Supabase Auth (D5).

**Before enabling Supabase Auth** (required, D5): in the dashboard, create a new Supabase-managed standby key and **Rotate key** to it. Your imported key becomes "previously used" and stays trusted, so AppOps tokens keep working, and Auth signs user sessions with a key that never leaves Supabase. Do not revoke the imported key while any app has `api.enabled`. Confirm an app's REST call still succeeds after the rotation. This relies on Phase 0 (f).

Do not revoke the legacy JWT secret while anything still uses a legacy key. Check which kind of key the worker's `SUPABASE_SERVICE_ROLE_KEY` is (a legacy JWT or an `sb_secret_` key) before any revocation.

Enabling an app: add the `api` block (and `permissions.functions` if the app calls RPCs), `./run.sh preview`, review that only the expected resources appear (authenticator grant, function grants if requested, one secret), then `up`. The project's REST schema exposure (hosted dashboard setting and `[api] schemas` in `db/supabase/config.toml`) is still a manual prerequisite: `metrics_worker` is exposed today, `shortener` is not.

Rotating a token: set `api.issued_on` to today, preview, `up` (token only; `credentials.version` rotates the password). The worker reads the secret at request time, so no worker redeploy is needed (per `OPERATIONS.md`, **Untested**).

Kill switch for one app: set `api.enabled: false` (removes the membership and the secret) or run `REVOKE <app role> FROM authenticator` by hand for an immediate stop. A token stays cryptographically valid until its `exp` even after the secret is deleted; what stops it working is the removed membership. Revoking the signing key in Supabase invalidates every app's token at once.

## 7. Risks and what to do about them

| Risk | Consequence | Mitigation |
| --- | --- | --- |
| The signing key can mint a token for **any** role, including `service_role` | Whoever holds it owns the project's API. AppOps (and the operator's machine) becomes the most sensitive component | Keep it out of git and logs, load it only when an app opts in, treat the AppOps environment as production-secret, document it |
| Making the imported key current changes what Supabase Auth signs with | Project-wide change made for a per-app feature | Phase 0 on a separate project; no effect while Supabase Auth is unused; rotate to a Supabase-managed key before enabling Auth (D5) |
| The imported key is "previously used" and someone revokes it while tidying keys | Every app's REST token stops working at once | Document in `OPERATIONS.md` that the key must not be revoked while any app has `api.enabled` |
| Expiry with manual applies | If nobody runs `up` before `exp`, the app's REST calls fail with an auth error | Default 180 days; export the expiry; changing `api.issued_on` re-mints without a password rotation; AppOps warns within 30 days and refuses an expired token; document a calendar reminder. A scheduled job is a possible follow-up, not in scope |
| Hosted Supabase may restrict `GRANT <role> TO authenticator` | The whole approach fails for that app | Phase 0 tests it before any code |
| PostgREST or Supabase may reject `iss` or require `aud` | Requests rejected | Test in Phase 0 and adjust the claims in `apijwt` |
| RPCs revoke `EXECUTE` from `PUBLIC` (known for metrics-worker and shortener) | RPCs fail with a permission error | `permissions.functions` in this plan |
| RLS with no policy for the app role (true today for every `metrics_worker` table) | Direct table reads silently return `[]`; writes fail | Use RPCs, or add policies in migrations (section 9). AppOps does not manage RLS |
| Token not marked secret | Plaintext token in state or preview | The Cloudflare SDK wraps `Value` as secret; AppOps wraps it too, and a mock test asserts the outcome |
| `Mint` output is not stable | Cloudflare secret changes every run | `Sign(nil, ...)` (RFC 6979) and the determinism test (D2) |
| Removing `api.enabled` later | Drops the grant and the secret | Intended; the role and schema stay protected as today |

## 8. Build order

Phase 0, a manual spike before any code (you run it; it needs your say-so). Run it on a **separate Supabase project or branch**, not production, because step (b) requires making the imported key current, which changes Supabase Auth signing for the whole project. With a throwaway role and key, confirm:

- (a) `GRANT <role> TO authenticator` works on a hosted Postgres 17 project when run as `postgres`.
- (b) A token signed ES256 with the imported, current key and `role: <role>` is accepted by the REST API alongside a publishable `apikey`.
- (c) Which claims are required, and whether `iss: "appops"` is accepted.
- (d) RPC behavior: `SECURITY DEFINER` functions with `EXECUTE` revoked from `PUBLIC` fail as the role, and succeed after `GRANT EXECUTE`.
- (e) Schema exposure behavior, and the effect of rotating the signing key on Supabase Auth and on the legacy keys.
- (f) After rotating to a new Supabase-managed key, whether a token signed with the now "previously used" imported key is still accepted. Required even though it is not needed yet: it is the step to take before enabling Supabase Auth (D5).

Write the results into this document. If (a) or (b) fails, stop and rethink. If ES256 fails but RS256 works, switch D2.

### Phase 0 results: local stack, 2026-11-13

Setup: Supabase CLI 3.1.0, an isolated copy of `db/supabase` with its own `project_id` (no link to the hosted project), PostgreSQL 17.11, all four repository migrations applied, an ES256 key from `supabase gen signing-key` loaded through `[auth] signing_keys_path`. A role `app_metrics_worker_dev` was created with the grants AppOps gives metrics-worker. Tokens were signed by a throwaway Go program using `Sign(nil, ...)` and the ASN.1-to-raw conversion. The stack, its volumes and the key were deleted afterwards.

| Check | Result |
| --- | --- |
| (a) `GRANT <role> TO authenticator` as `postgres` | **Pass.** `postgres` is not a superuser but holds `ADMIN` on the role (granted automatically by `supabase_admin`). Membership: `set_option` true, `inherit_option` false, as PostgREST needs |
| (b) ES256 token with `role: app_metrics_worker_dev` | **Pass.** `current_user` inside the request is the app role. Same inputs give the same token |
| (c) claims | `iss: "appops"` accepted. Expired token: `401 PGRST303`. Wrong `kid` or tampered signature: `401 PGRST301`. Unknown role: `401`. Existing role not granted to `authenticator`: `403 permission denied to set role` |
| (d) RPCs and RLS | RPC without `EXECUTE`: `403`. After `GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA`: success. Table reads: `200 []` because policies cover only `anon` and `authenticated` |
| (e), (f) | **Not testable locally** (hosted key states and Auth) |

Local-only behavior that must not be relied on: the local gateway accepted requests **without an `apikey`** and tokens **without `exp`**. Hosted Supabase documents both as required; keep sending both.

Still needed on a hosted (throwaway) project before Phase 7: (b) with a key **imported through the dashboard** and made current; whether the hosted gateway enforces `apikey` and `exp`; (e); (f).

Unrelated finding: on the local stack, `anon` and `authenticated` hold `EXECUTE` on `metrics_worker.record_site_events` and `metrics_worker.record_worker_health`, and calls with **only the publishable key** succeeded. See section 9.

Phase 1: config options (`api`, `permissions.functions`), validation and tests. Phase 2: `apijwt` package and tests. Phase 3: authenticator grant, function grants and tests. Phase 4: token publication and tests. Phase 5: `main.go` wiring, exports. Phase 6: documentation. Each phase leaves the existing flow unchanged for apps without the new options. Phase 7, live verification: preview shows only the expected additions for one app; `up`; the secret exists; a real REST call and a real RPC call succeed as the app's role; a call to another schema fails.

## 9. Follow-ups outside this plan

- metrics-worker cutover: add `api` and `permissions.functions: [EXECUTE]` to its `appops.yaml`, add a `[[secrets_store_secrets]]` binding, read the token with `await env.API_TOKEN.get()`, send it as `Authorization: Bearer` with the publishable key as `apikey`, then delete the `SUPABASE_SERVICE_ROLE_KEY` Wrangler secret. The Secrets Store binding is **Untested** and not available under `wrangler dev`. Before cutover, decide how the worker reads tables: the existing "public can read" policies cover only `anon` and `authenticated`, so reads as the app role return `[]`. Either read through `SECURITY DEFINER` RPCs or add policies for the app role in a migration. A policy naming `app_metrics_worker_<env>` directly breaks on databases where that role does not exist (local, CI), so a `NOLOGIN` group role granted to the app role, or a guarded `DO` block, is likely needed; design that separately.
- **Security (pre-existing, outside this plan):** `anon` and `authenticated` can execute the metrics-worker write RPCs. The migrations revoke `EXECUTE` from `PUBLIC` only, while Supabase's default privileges grant it to those roles explicitly. On the local stack a request with only the publishable key called both functions successfully. Check the hosted project read-only (`select proname, proacl from pg_proc where pronamespace = 'metrics_worker'::regnamespace;`) and, if confirmed, add a forward migration that runs `REVOKE EXECUTE ... FROM anon, authenticated`. The shortener RPCs use the same pattern and need the same check.
- URL shortener: separately decide REST (this feature) or direct Postgres through Hyperdrive. The Hyperdrive experiment is parked on `wip/metrics-worker-hyperdrive`. If REST, the `shortener` schema must also be exposed.

## 10. Decisions

All decisions confirmed by the owner on 2026-11-13.

- **D1 (confirmed)** Scoped role token, not `service_role` power (section 1). Requires `permissions.functions` for apps that use RPCs.
- **D2 (confirmed)** ES256 with deterministic RFC 6979 signing from the Go standard library (section 3). RS256 only as a fallback if Phase 0 rejects ES256.
- **D3 (confirmed; revised 2026-11-13 in Phase 5)** Token rotation: `api.issued_on` in `appops.yaml` anchors `iat` and `exp`; changing it re-mints the token without rotating the database password, and the expiry is exported. Replaces the original `time.Static` keyed to `credentials.version` and `api.version`, after Phase 5 found `pulumi-time` unmaintained since 2024 (section 5). Automated rotation (a scheduled job, or a read-only expiry check) is deferred; it needs its own threat model because a scheduled `up` would put the signing key in CI.
- **D4 (confirmed)** Default expiry 180 days, range 7 to 365. Supabase requires an `exp` claim, so tokens always expire; the range is this plan's choice and can be widened later.
- **D5 (confirmed)** Leave the imported key current (A) while the project does not use Supabase Auth. **Before enabling Supabase Auth**, rotate to a new Supabase-managed key (B), so user sessions are never signed with a key held outside Supabase; the imported key stays trusted as "previously used" and AppOps tokens keep working. The imported key can mint any role either way, so B only protects user sessions. B depends on "previously used" trust, which Phase 0 (f) must confirm even though B is not needed yet; if (f) fails, revisit how AppOps signs tokens before adding Auth.
