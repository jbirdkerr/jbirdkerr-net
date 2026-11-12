# AGENTS.md

Static JavaScript/WebAudio dog-photo app, deployed to Cloudflare Pages as `boopmaster3000`. Read [`README.md`](README.md) and [`docs/`](docs/) first; repository-wide rules are in the root [`AGENTS.md`](../../AGENTS.md).

## Commands

- No build step. Serve locally with `python3 -m http.server` from this directory.
- Deploys through `.github/workflows/deploy.yml` on pushes to `main` when this unit is affected. Do not deploy unless asked.

## Layout

- `index.html`: semantic skeleton only.
- `css/style.css`: layout, animations, modals, retro styling.
- `js/main.js`: PostHog init, WebAudio synthesis, eye tracking, particles, modals, keyboard shortcuts.
- `wrangler.toml`: Pages project; `pages_build_output_dir = "."`.

## Conventions

- Vanilla HTML, CSS and JS. No framework, bundler or build system unless asked.
- Keep markup in `index.html`, styling in `css/style.css`, behavior in `js/main.js`.
- Audio is synthesized with WebAudio and must start only after a user gesture. No autoplay, no audio asset files.
- New tracked events go through `captureEvent()` and follow the naming in [`docs/analytics-events.md`](docs/analytics-events.md). Update that file in the same change.
- Metrics go through the existing worker and Supabase pipeline (`https://metrics-api.jbirdkerr.net`). Do not add trackers, new third-party scripts or personal-data collection without an explicit request.
- If the worker's `/metrics` or `/health-metrics` response shape changes, update the stats panel markup and polling in `main.js` in the same change. The worker caches `/metrics` for about 3 seconds.
- A new page origin must be added to the worker's CORS allow-list in `apps/metrics-worker/worker.py`, or the panel and analytics fail from that origin.
- Respect `prefers-reduced-motion` for any new animation. The existing code does not handle it yet.

## Things to know before editing

- Everything in this directory is published by Pages (`pages_build_output_dir = "."`), including Markdown files, and any change here redeploys the site. Never add secrets, notes or private files.
- The PostHog project key in `js/main.js` is a public client key, not a secret.
- There are no automated tests. Verify canvas, audio and panel changes in a browser and say what you could not check.
- Pre-commit covers formatting only: `uv run pre-commit run --all-files`.
