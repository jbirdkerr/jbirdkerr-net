# Boopmaster 3000

An interactive dog photo: eyes that follow the pointer, a snoot to boop, googly eyes, treat showers and a party mode, all with synthesized sound. A stats panel shows live usage from the metrics pipeline.

Live at `https://boopmaster3000.jbirdkerr.net` (Cloudflare Pages).

## Controls

| Input | Action |
| --- | --- |
| Click the snoot, or `B` | Boop |
| Click the photo | Toggle eye tracking (`E` also toggles it) |
| Double-click or double-tap, or `G` | Toggle googly eyes |
| `P` | Toggle party mode |
| `T` | Treat shower |
| `W` | Bark |
| `M` | Open or close the stats panel |
| `Esc` | Close open panels |

The same actions are available from the browser console as `boop()`, `bark()`, `treat()`, `party(on)`, `eyes(on)` and `googly(on)`, or through `window.HenryBeard`.

## Layout

```text
apps/boopmaster/
├── index.html        # Semantic skeleton: stage, modals, controls
├── css/style.css     # Layout, animations, modals, retro styling
├── js/main.js        # PostHog init, WebAudio, eye tracking, particles, modals, shortcuts
├── img/              # henrybeard.png (displayed), henrybeard.jpg (source)
├── wrangler.toml     # Cloudflare Pages project (boopmaster3000)
├── BUILD             # Pants targets: archive of the site files
└── docs/             # Architecture and analytics notes
```

There is no build step and no framework. HTMX is loaded from `unpkg.com` for the stats panel.

## Run locally

```bash
cd apps/boopmaster
python3 -m http.server 8000     # then open http://localhost:8000
```

Audio starts only after a user gesture. The stats panel and analytics call `https://metrics-api.jbirdkerr.net`, which only accepts requests from its allowed origins (see `apps/metrics-worker/worker.py`), so a `localhost` page will not load live stats.

## Deploy

The main workflow (`.github/workflows/deploy.yml`) runs `wrangler pages deploy apps/boopmaster --project-name boopmaster3000 --branch main` on pushes to `main` when the `boopmaster` unit in `deploy/units.toml` is affected, which is any change under `apps/boopmaster/`. It skips the deploy when `CLOUDFLARE_API_TOKEN` is not set. Do not deploy by hand unless you mean to.

**Everything in this directory is published.** `wrangler.toml` sets `pages_build_output_dir = "."`, so files such as `README.md`, `AGENTS.md` and `docs/` are served from the site, and changing them redeploys it. Do not put anything private here.

## How it connects to the rest of the repo

Events captured by the PostHog SDK go through the metrics worker proxy, which forwards them to PostHog and records them in Supabase. The stats panel reads HTML fragments from the same worker. See:

- [docs/architecture.md](docs/architecture.md): the client side of the system
- [docs/analytics-events.md](docs/analytics-events.md): the events this page sends
- [`apps/metrics-worker/docs/`](../metrics-worker/docs/): the worker, API and pipeline

## Known limitations

- `css/style.css` and `js/main.js` have no `prefers-reduced-motion` handling.
- The credits panel in `index.html` still says the site is hosted on GitHub Pages; it is on Cloudflare Pages.
- HTMX is loaded unpinned from `unpkg.com`.
- Behavior (canvas, audio, panels) can only be checked in a browser; there are no automated tests for this app.
