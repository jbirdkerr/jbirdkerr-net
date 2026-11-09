# DigDog: Handoff for the Implementing Agent

You are picking up a small WebAssembly game that was designed and partly written in an earlier session. This document is the full context. Read it end to end before changing anything.

**Status in one line:** the design is settled, the game logic and wiring are written in Go (Ebiten), but **nothing has been compiled or run yet** and **there is no art yet**. Your first job is to get it building and running, then work through the plan below.

---

## 1. Goal

Build a dog-themed card-matching game that compiles to WebAssembly and is embedded on the owner's personal site, `jbirdkerr.net`.

- A pixel-art dog sprite walks to a dirt mound, digs up a card, then digs up a second one.
- Two matching cards are cleared. A mismatch buries both again after a short pause.
- Visual style: **32-bit-era pixel art** (small internal resolution, crisp scaling, limited palette).
- It is a fun personal experiment, not a commercial product. Keep scope small and finish it.

## 2. Context about the owner and the site

- Site: `jbirdkerr.net`, a static **Astro** blog/portfolio (Tailwind, dark terminal/retro look, JetBrains Mono, blue/cyan glow). This game lives in the same **monorepo** as the site (`github.com/jbirdkerr/jbirdkerr-net`), in `apps/digdog/`, and is served from its own subdomain.
- The monorepo's Pages site publishes `jbirdkerr.net` via GitHub Actions. An existing experiment (a dog photo with eye tracking and snoot booping, "Henry's snoot booper") lives at `henry.jbirdkerr.net`. Its stack is vanilla JS and WebAudio, with a Cloudflare Worker for analytics.
- Hosting philosophy: **as free as possible** (GitHub Pages, Cloudflare free tier). The owner cares about accessibility and affordability of technology, so the game should be cheap to host and cheap to understand. Pragmatism over cleverness; don't add complexity to justify a technology.
- The owner's site conventions (from the repo's `AGENTS.md`) that apply here too. Note that `AGENTS.md` may still describe the older single-page site layout; follow its conventions, not its file layout:
  - Make small, focused changes. Prefer editing existing files over adding new ones.
  - Don't add a heavy build system, framework, or large dependency without asking.
  - **Privacy-conscious:** do not add third-party trackers or collect new personal data unless asked.
  - Browsers require a user gesture before audio starts. Never autoplay.
  - Licensed **AGPL-3.0**. Don't add code, assets, or dependencies with incompatible licenses (check licenses of any art, fonts, or sounds you bring in).
  - Never commit secrets or `.env` files.
  - Verify frontend changes in a real browser; reading code is not enough for canvas, audio, and touch behavior.
  - Summarize what you changed and flag anything you could not test.

## 3. Decisions already made

| Decision | Choice |
| --- | --- |
| Name | **DigDog** |
| Location | Monorepo `github.com/jbirdkerr/jbirdkerr-net`, directory `apps/digdog/`, with its own `go.mod` |
| Language / engine | **Go with Ebiten v2** (`github.com/hajimehoshi/ebiten/v2`) |
| Target | WebAssembly (`GOOS=js GOARCH=wasm`), also runnable natively for development |
| Game concept | Card-match; a dog "digs up" two cards per turn |
| Art style | 32-bit-style pixel art, internal resolution 320×180, scaled up with nearest-neighbor filtering |
| Sprite sizes | Dog frames are 32×32; cards/mounds are drawn in a 44×44 cell with a 36×36 card |
| Input | Mouse click and touch tap on a mound |
| Hosting | Static files on **Cloudflare**, served from the subdomain **`digdog.jbirdkerr.net`** (GitHub Pages cannot serve a second custom domain from this repo) |

Alternatives that were considered and not chosen: Python via pygbag/Pyodide (heavy first load, slower frame loop), Rust (steeper learning curve), Godot (large runtime), plain JS canvas.

## 4. Current state of the code

Two files exist: `main.go` and `cards.go`. They belong in the same `package main` in `apps/digdog/`, next to the game's own `go.mod`.

**They have never been compiled.** Expect possible typos or API drift. Fix compile errors first and keep fixes minimal.

The owner has already created `apps/digdog/go.mod`:

```
module github.com/jbirdkerr/jbirdkerr-net/digdog

go 1.26.8

require github.com/hajimehoshi/ebiten/v2 v2.10.5
```

(plus indirect dependencies). Two things to know:

- Ebiten **v2.10.5 is newer than the version the code was written against**. If an API call fails to compile or is deprecated, check the Ebiten docs for that version and adapt.
- The module path ends in `/digdog`, but the directory is `apps/digdog`. The convention is for the path to match the directory, so change it once, early, with `go mod edit -module github.com/jbirdkerr/jbirdkerr-net/apps/digdog`. Nothing imports the module, so this is safe. Ask the owner first if they would rather keep the current path.

### Required asset that does not exist yet

`main.go` has `//go:embed assets/dog.png`. **The build fails until that file exists.** Create a placeholder first (see Phase 0).

### Sprite sheet contract (`assets/dog.png`)

One PNG, 32×32 frames, 4 columns by 5 rows, so **128×160 pixels**. Each row is one animation, read left to right.

| Row | Name | Frames | Loops | Ticks per frame | Notes |
| --- | --- | --- | --- | --- | --- |
| 0 | idle | 4 | yes | 15 | Tail wag |
| 1 | dig | 4 | no | 6 | Paws and dirt flying |
| 2 | sniff | 3 | no | 10 | Nose down, head turn (not wired into gameplay yet) |
| 3 | happy | 3 | no | 8 | After a match |
| 4 | sad | 2 | no | 12 | After a miss |

Ebiten updates at 60 ticks per second by default, so 6 ticks per frame is 10 fps.

### Architecture

**`cards.go`: pure game rules, no dog knowledge.**

- `Board`: grid of `Card{Face int, State CardState}`. States are `Buried`, `Revealed`, `Matched`.
- `NewBoard(cols, rows)`: builds a shuffled deck of pairs. **Cell count must be even** (it panics otherwise).
- Methods the game loop calls:

| Method | Purpose |
| --- | --- |
| `CellAt(x, y) int` | Card index under a screen point, or -1 |
| `CellCenter(i) (x, y float64)` | Centre of a cell, used as the dog's destination |
| `Pick(i) bool` | Reserve a buried card for digging. Returns false if locked, out of range, or not buried. Sets `locked` |
| `Reveal(i) Result` | Flip the card face up and resolve the turn |
| `Update()` | Counts down the miss timer and re-buries a mismatched pair |
| `Draw(screen)` | Draws placeholder rectangles for mounds, faces, and cleared holes |

- `Result` values: `ResultFirst`, `ResultMatch`, `ResultMiss`, `ResultWin`.
- Lock rules: `Pick` locks the board. `Reveal` unlocks it after a first card or a match. After a miss it stays locked until `Update` re-buries the pair (45 ticks). After a win it stays locked.
- Placeholder art: each face has a color plus a **pip count** (face index + 1 small squares), so pairs are distinguishable without color vision. Keep this accessibility property when real art arrives (a symbol, shape, or number on each card face).
- Constants: `cellW=cellH=44`, `cardW=cardH=36`, `missTicks=45`.

**`main.go`: dog, animation, input, loop.**

- `Animation`: frame slice, `ticksPerFrame`, `loop`, `Update/Reset/Frame/Done`.
- `Game`: owns the animation map, the `Board`, the dog position (`dogX, dogY`, the sprite centre), `facing` (1 or -1), `target`, and the flags `walking` and `digging`.
- Turn flow in `Update`:
  1. `board.Update()`.
  2. If the dog is free and there was a click or tap, `CellAt` then `Pick`. On success, set `target` and `walking = true`.
  3. `walk()` moves the dog at 2 px per tick toward a point 16 px below the mound centre. On arrival, `digging = true` and the `dig` animation starts.
  4. When the dig animation finishes, call `board.Reveal(target)`, then `afterReveal` plays `happy` (match or win), `sad` (miss), or `idle`.
- `pointerPos()` returns a fresh mouse click or touch in logical coordinates.
- `R` restarts the game. The HUD uses `ebitenutil.DebugPrintAt` for the move counter and win message.
- `Layout` returns a fixed 320×180 logical size.

### Controls today

Click or tap a mound; `R` restarts. There is no keyboard play, no audio, no menu.

### Known issues and risks

1. **Never compiled or run.** Verify Ebiten API usage (`inpututil.AppendJustPressedTouchIDs`, `ebitenutil.DebugPrintAt`, `SubImage(...).(*ebiten.Image).Fill`) against the Ebiten version you install.
2. The dog is drawn over the row of mounds below its target, which may look cluttered. Tune the `cy+16` offset in `walk()` or the board origin if so.
3. Touch and mouse input may both fire on some devices. The board lock should prevent double picks, but test it.
4. Non-integer scaling in the browser will make pixels uneven (see Phase 1).
5. Restart is keyboard-only (`R`), which excludes touch users after winning.
6. No keyboard-only way to play yet.

## 5. Plan

Work in order. Each phase should leave the game in a working, committable state. Keep commits small.

### Phase 0: Make it build and run natively

1. Work from `apps/digdog/`. The module already exists (see section 4). Fix the module path if the owner agrees, then run `go mod tidy`.
2. Generate a **placeholder `assets/dog.png`** (128×160, 4×5 grid of 32×32 frames, a distinct solid color per row, a visible frame-number marker so animation timing can be seen). A tiny throwaway Go or Python script is fine; keep it out of the shipped game or put it under a `tools/` folder.
3. Run `go vet ./...` and `go run .`. Fix compile errors.
4. Play a full game: click mounds, confirm walk, dig, reveal, match, miss, re-bury, win, and `R` restart.

**Done when:** a full round plays natively with placeholder art and no panics.

### Phase 1: WebAssembly build and page

1. Build:
   ```bash
   GOOS=js GOARCH=wasm go build -o web/digdog.wasm .
   cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" web/
   ```
   (Before Go 1.24 the file is at `misc/wasm/wasm_exec.js`.) **`wasm_exec.js` must come from the same Go version that built the `.wasm`.** Pin the Go version in CI by reading it from `go.mod` (for example `actions/setup-go` with `go-version-file: apps/digdog/go.mod`) so the toolchain and `wasm_exec.js` always match.
2. `web/index.html`: load `wasm_exec.js`, then `WebAssembly.instantiateStreaming(fetch("digdog.wasm"), go.importObject)` and `go.run(...)`. Match the site's dark background (`#0a0e27`), `<meta name="viewport">`, a `lang` attribute, a `<title>`, and a short `<noscript>` message.
3. Serve `web/` locally (`python -m http.server` inside it) and test in a desktop browser and a phone-sized viewport.
4. **Crisp scaling:** pixel art must scale by whole numbers where possible. Ebiten scales the logical 320×180 screen to the canvas; check whether pixels look uneven at the browser's window size and, if so, implement integer scaling (for example by adjusting what `Layout` returns). Verify the approach against the Ebiten docs for the installed version before committing to it.
5. Check the `.wasm` size. If it is too large for comfortable loading on mobile, try compressing at the host (Cloudflare normally compresses static assets automatically; confirm in the deployed response headers). TinyGo may shrink the binary, but **verify current Ebiten support for TinyGo before relying on it**; do not switch toolchains without checking.

**Done when:** the game runs in a desktop browser and on a mobile viewport with crisp pixels and working touch.

### Phase 2: Real art

The owner wants 32-bit-style pixel sprites. Either the owner supplies art or you help them make it.

1. Finalize `dog.png` to the sprite contract in section 4. The dog is meant to be recognizable as the owner's dog, Henry; the owner has reference photos. Palette: roughly 16 to 32 colors.
2. Create `cards.png` with a mound sprite (buried), a card back/face frame, a set of face symbols (one per pair, distinct by **shape**, not color alone), and a cleared-hole sprite. Replace the rectangle drawing in `Board.Draw` with sprite drawing, keeping the same cell geometry or updating the constants together.
3. Suggested free tools: LibreSprite (open source), Piskel (browser-based). Embed assets with `//go:embed`.
4. If you generate or source art, keep licensing compatible with AGPL-3.0 and note the source in a `CREDITS` or README section.

**Done when:** the game uses real sprites with no placeholder rectangles, and every card face is distinguishable without relying on color.

### Phase 3: Gameplay polish (keep each item small and optional)

Pick in roughly this priority, and stop when the game feels finished:

1. **Dirt particles** on dig and a small card-flip animation. Keep it simple and respect reduced-motion (see Phase 5).
2. **Combo streak:** consecutive matches build a treat multiplier; a miss resets it. Show it in the HUD.
3. **Sniff power-up:** the `sniff` animation already exists. Sniffing briefly reveals a random card or row, at a cost (a turn, or a limited number of uses).
4. **Levels:** each win grows the board (for example 4×3, then 4×4, then 5×4, always with an even cell count) and adds more face symbols.
5. **Buried bones:** a few mounds hold bones instead of cards, giving bonus points or a free peek. Only add this if the earlier items feel good.
6. A simple **win screen with a tap or click to play again** (restart currently needs a keyboard).

Keep game rules in `cards.go` and animation/input wiring in `main.go`, as they are now.

### Phase 4: Audio

1. Use Ebiten's audio package. Prefer **synthesized sounds** (generated waveforms) over audio asset files; the owner's other site does this with WebAudio. Short effects: dig scratch, card reveal, match chime, miss, win.
2. Browsers block audio until a user gesture. Start the audio context on the first click or tap, never on load. Provide a **mute toggle** that is reachable by touch and keyboard, and default to a modest volume.

### Phase 5: Accessibility and input

This matters to the owner's site theme (technology that is accessible to everyone).

1. **Keyboard play:** arrow keys move a cursor between mounds; Enter or Space digs. Show a visible focus indicator on the selected mound.
2. Respect `prefers-reduced-motion` where the host page can signal it (a small JS bridge or a query parameter is acceptable); otherwise provide an in-game toggle for particles and screen effects.
3. Never rely on color alone (already true for the pips).
4. On the HTML page, add a short text description and instructions for the game, and keep the page usable with scripts disabled (a clear message).

### Phase 6: Deploy to the `digdog.jbirdkerr.net` subdomain

**Why Cloudflare:** a GitHub Pages site supports one custom domain per repository, and this repo's Pages site already serves `jbirdkerr.net`. The game therefore deploys to Cloudflare's static hosting instead. Cloudflare's static-hosting products have been evolving (Pages and Workers static assets), so **check Cloudflare's current docs** and pick whichever they recommend for a static folder. Both serve `.wasm` with the right MIME type.

**Recommended approach: build in GitHub Actions, deploy the prebuilt folder with Wrangler.** Cloudflare's own build image may not offer the pinned Go version (1.26.8), so building in Actions, where `setup-go` can read `go.mod`, avoids that problem and keeps the toolchain and `wasm_exec.js` matched.

1. Add a workflow, for example `.github/workflows/digdog.yml`, that:
   - triggers only on changes under `apps/digdog/**` (and the workflow file itself), plus manual dispatch, so game changes don't redeploy the Astro site and vice versa;
   - sets up Go with `go-version-file: apps/digdog/go.mod`;
   - runs `go vet ./...` and builds the wasm with `working-directory: apps/digdog`;
   - copies the matching `wasm_exec.js` from `$(go env GOROOT)/lib/wasm/` into `web/`;
   - deploys `apps/digdog/web/` to Cloudflare with Wrangler.
2. Do not commit built output. Add `apps/digdog/web/digdog.wasm` and `apps/digdog/web/wasm_exec.js` to `.gitignore`.
3. Add a Wrangler config for the static assets (in `apps/digdog/`) and attach the custom domain `digdog.jbirdkerr.net`. Custom domains on Cloudflare generally require the zone's DNS to be on Cloudflare. The owner's metrics worker already uses a `jbirdkerr.net` subdomain, which suggests it is, but confirm.
4. **The owner must create the Cloudflare API token and account ID and add them as GitHub repository secrets.** The workflow reads them as secrets. Never put them in files, and do not run a production deploy or change DNS yourself unless the owner explicitly asks. Write the workflow, explain which secrets it expects, and stop there.
5. Check that the Astro tooling ignores `apps/`. Astro reads `src/`, so this should be fine, but if `astro check` or a TypeScript config picks up `apps/digdog/web/wasm_exec.js`, add `apps/**` to the config's `exclude`.
6. Do not add Cloudflare Workers or analytics logic to the game. It is fully static.

**Done when:** a push that changes the game deploys it to `https://digdog.jbirdkerr.net`, a change elsewhere in the repo does not, and no secrets are committed.

### Phase 7: Link it from the main site (hand this to the owner, or do it only if asked)

The main site lists experiments through a content collection at `src/content/projects/`. Each entry is a Markdown file with front matter validated by a schema:

```yaml
---
title: DigDog
description: A pixel-art card-matching game where a dog digs up the cards. Go, Ebiten, WebAssembly.
url: https://digdog.jbirdkerr.net
repo: https://github.com/jbirdkerr/jbirdkerr-net/tree/main/apps/digdog
stack: [Go, Ebiten, WebAssembly]
order: 2
---
```

`url` and `repo` must be valid URLs, `stack` is a list of strings, and `order` is a number (lower sorts first). This file lives in the same monorepo but outside `apps/digdog/`, so make that change only if the owner asks, as its own small commit.

An optional blog post about building the game would fit the owner's writing style: first-person engineering notes with real trade-offs and what went wrong. Don't write it unless asked.

## 6. Analytics: explicitly out of scope unless asked

The main site sends events to PostHog through a Cloudflare Worker proxy, and the owner is privacy-conscious. **Do not add any analytics, tracking, or network calls to this game** unless the owner asks. If asked later, route events through the existing worker and follow the existing event naming convention on the main site.

## 7. Definition of done

- [ ] Builds natively and to WebAssembly with a pinned Go version.
- [ ] A full round is playable on desktop and on a phone (touch).
- [ ] Pixel art is crisp at the displayed size.
- [ ] Real sprites replace all placeholders; pairs are distinguishable without color.
- [ ] Mouse, touch, and keyboard all work; restart does not require a keyboard.
- [ ] Audio only starts after a user gesture and can be muted.
- [ ] No third-party trackers and no secrets.
- [ ] Deployed to `digdog.jbirdkerr.net` via a path-filtered CI workflow, serving the wasm correctly, with no secrets committed.
- [ ] README covers what the game is, how to run it locally, how to build the wasm, and asset credits and licensing.
- [ ] You have listed anything you could not test.

## 8. Open questions for the owner

Ask these rather than guessing:

1. Is DNS for `jbirdkerr.net` managed in Cloudflare (needed for a custom domain there)?
2. Cloudflare Pages or Workers static assets? Default to whichever Cloudflare's current docs recommend, unless the owner has a preference.
3. Will the owner draw or commission the sprites, or do you generate pixel art? (Affects Phase 2.)
4. Should the game have levels and power-ups, or stay as a single short round?
5. Should the dog be based on Henry's real markings and colors?

## 9. Working agreements

- Make small, reviewable commits.
- Keep changes inside `apps/digdog/` and the game's own workflow file. Touch the Astro site, other workflows, or shared config only if asked.
- Do not deploy, change DNS, or create or handle secrets without being asked.
- Do not add heavy dependencies or switch engines.
- Report plainly what you verified (ran in a browser, tested on touch) and what you only reasoned about.

## 10. Working with a small local model

If you are a small local model (for example qwen2.5-coder through Ollama), do not load this whole document. Read `AGENTS.md` and one file from `tasks/` per session instead. `check.sh` is the main safety net, `build-wasm.sh` builds for the browser, and `tasks/ESCALATE.md` lists what should go to a stronger model. The `Modelfile` shows a suggested context size and temperature.
