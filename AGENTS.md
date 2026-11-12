# AGENTS.md

Repository-wide guidance for AI agents working in this monorepo. For the human-facing overview, see [`README.md`](README.md). Before editing a unit, read its nearest `AGENTS.md` and relevant README or docs.

## Verify current behavior

- Treat tracked source, tests, and `.github/workflows/` as evidence of what runs today. `repo-spec.md` is marked draft and contains design proposals and older assumptions; use it for intent, not as proof of current deployment behavior.
- The main workflow, [`.github/workflows/deploy.yml`](.github/workflows/deploy.yml), validates pull requests and pushes to `main`. Its deploy job runs only for `main` pushes or manual dispatch and has steps for Supabase migrations, Boopmaster, DigDog, the metrics worker, and the blog. The Supabase migrations step is normally a no-op (no `SUPABASE_ACCESS_TOKEN` in CI): the Supabase GitHub integration applies `db/supabase/migrations/` when changes reach `main`, independently of this workflow, so migration-before-deploy ordering is not guaranteed. The `db` unit still matters because `metrics-worker` depends on it in `deploy/units.toml`.
- DigDog's Go vet/tests and WebAssembly vet/build run in the main validation job. Its production deployment downloads the validated build artifact and runs only when `deploy/units.toml` marks `digdog` as affected. The manifest detects changed units; it does not create build or deploy steps by itself.

## Project map

| Unit | Path | Guidance and current role |
| --- | --- | --- |
| Main site | `blog/` | Astro blog and portfolio. Read `blog/AGENTS.md`; keep its public routes and CNAME stable. |
| Boopmaster | `apps/boopmaster/` | Static JavaScript/WebAudio dog-photo app; deployed to Cloudflare Pages by the main workflow. |
| DigDog | `apps/digdog/` | Go/Ebitengine game compiled to WebAssembly; read `apps/digdog/README.md`; checked and deployed by the main workflow. |
| Metrics worker | `apps/metrics-worker/` | Python Cloudflare Worker, PostHog proxy, Supabase metrics API, tests, and docs. Its tables and RPCs live in the `metrics_worker` schema; it reaches them over PostgREST with `Accept-Profile` / `Content-Profile` headers and the service-role key. |
| Database | `db/supabase/` | Supabase config, schemas, seed data, and timestamped PostgreSQL migrations. |
| URL shortener | `apps/url-shortener/` | Design spec only; no service implementation or active deployment workflow yet. |
| AppOps | `infra/appops/` | Pulumi (Go) project that provisions per-app PostgreSQL schemas, roles and grants and publishes credentials to Cloudflare Secrets Store. Applied by hand, not by CI. Read `infra/appops/AGENTS.md` first. |
| Deployment tooling | `deploy/`, `scripts/` | Main workflow's unit manifest and affected-path detection. The `appops` unit is detection-only (no build or deploy step). |

## Working agreements

- Make focused, reviewable changes and match the unit's existing patterns. Verify paths, commands, and deployment claims against the current tree and workflow before documenting them.
- Never commit `.env` files, credentials, tokens, or service-role keys. Keep secrets server-side and do not print secret values in logs or shared output.
- Do not run production deployments, change DNS, or apply production migrations unless explicitly requested. A push to `main` can deploy affected units.
- Add database changes as new timestamped migration files. Never rewrite an applied migration; use a forward corrective migration instead.
- A new app schema: create it in the migration with `CREATE SCHEMA IF NOT EXISTS`. If the app uses the Supabase REST API, the schema must also be exposed on the hosted project and listed in `[api] schemas` in `db/supabase/config.toml` (developers with an existing local database then need a one-time `supabase db reset`). Apps that connect directly with their AppOps credential do not need exposure.
- Do not run `pulumi` or `infra/appops/run.sh` commands, and never print secrets from Pulumi state or `.env`, unless explicitly requested. These change the production database and Cloudflare. See `infra/appops/AGENTS.md`.
- Do not add trackers or collect personal data such as IP addresses or raw user agents without an explicit request. Boopmaster metrics should use the existing Worker and Supabase pipeline.
- For the blog, preserve `/blog/...`, `/projects/`, and `/about/` paths and keep `blog/public/CNAME`; Giscus depends on stable post paths. Follow `blog/AGENTS.md` for content and design conventions.
- For DigDog, follow its README: keep audio behind a user gesture, respect reduced motion, do not rely on color alone, and do not add analytics without an explicit request.
- The repository is AGPL-3.0. Check licenses for new code, packages, and assets before adding them.

## Validation

Run checks that cover the files changed; do not claim a check ran unless it did.

- Main CI runs `uv run pre-commit run --all-files`, script tests, and `./pants test ::`.
- Blog changes: `cd blog && npm ci && npm run build`.
- DigDog: main CI runs Go vet/tests and the WebAssembly vet/build on every workflow run. Locally, `cd apps/digdog && ./check.sh` runs the same core checks.
- The metrics-worker tests are included in pre-commit and CI; its tests live in `apps/metrics-worker/tests/`.
- AppOps: `cd infra/appops && gofmt -l . && go vet ./... && go test ./...`. CI does not run these, so run them for any change under `infra/appops/`.

When handing off, summarize the files changed, checks actually run, and any remaining deployment or environment-dependent verification.
