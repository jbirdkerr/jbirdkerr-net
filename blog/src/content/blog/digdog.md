---
title: "DigDog: Shipping a WebAssembly Game the Cheap and Simple Way"
description: Henry wanted to star in another project. So I built him a card-matching game in Go, compiled it to WebAssembly, and spent more time on the dog sprite than I planned to admit.
pubDate: 2026-10-28
tags: [go, webassembly, games, henry]
draft: false
---

Henry already has a subdomain. It is a canvas thing where you can boop his snoot and make him sneeze. I built it because I had a good photo, WebAudio turned out to be fun to synthesize sounds with, and it felt right that a personal site should have something interactive on it.

That project runs on vanilla JavaScript. It works, but it tops out at "one dog, one trick." A few weeks ago I started wondering what it would take to make Henry the star of his own game. Something with actual rules, a win condition, and the possibility of a losing move. That turned into DigDog.

## Why WebAssembly

The short answer: I wanted to write it in Go.

The longer answer: I have been trying to stretch my "Go muscles" lately and I like how it thinks. It has a real type system, fast compile times, and a standard library that doesn't surprise you. For a small game loop Go with [Ebitengine](https://ebitengine.org) felt like a very comfortable fit. Ebitengine handles the game loop, input, and rendering, and it compiles to WebAssembly with a single environment variable change:

```bash
GOOS=js GOARCH=wasm go build -o web/digdog.wasm .
```

That produces a `.wasm` binary. You drop it next to a thin `wasm_exec.js` shim that the Go toolchain ships, write about ten lines of HTML to load it, and the game runs in any modern browser with no plugin, no framework, no bundler.

One thing worth knowing: `wasm_exec.js` must come from *the same Go version that compiled the binary*. They are tightly coupled. I pin the Go version in `go.mod`, read it from there in GitHub Actions with `go-version-file`, and copy `wasm_exec.js` from the Go root at build time. That way the toolchain and the shim stay in sync regardless of what version happens to be installed on CI.

## What the Game Actually Is

It is a memory card-matching game. There is a 4×3 grid of dirt mounds. Click or tap a mound (or navigate with the keyboard) and Henry walks over, digs it up, and reveals the card underneath. Dig up a matching pair and they clear. Dig up a mismatch and both get re-buried after a short pause. Match all six pairs and you win.

The twist is the dog. Henry does not just pop cards up—he physically walks to the mound, plays a digging animation while dirt particles fly, then plays a happy or sad reaction depending on whether the cards matched. It adds maybe four seconds to each turn. I thought it would be annoying. It is not. It is adorable. And itt gives you a moment to actually think. Additionally, watching him droop after a miss is funnier than I expected.

## The Parts I Actually Struggled With

**The audio.** Browsers refuse to start an audio context until the user has interacted with the page. That is the right policy—no one wants a website that beeps at you on load—but it means you cannot initialize your audio system in `newGame()`. The solution is lazy initialization: the first click or keypress triggers `audio.NewContext(44100)` and the audio context lives from there. On Linux without ALSA or PulseAudio devices present (my development machine has no sound card in the environment where I was testing), the game detects the missing hardware and silently disables audio rather than crashing.

The sound effects themselves are synthesized from first principles: a short noise burst filtered with a low rumble for the dig scratch, ascending sine sweeps for card flips, stacked sinusoids decaying at different rates for the match chime and win fanfare. No audio files, no external library, just math. This is the same approach the snoot booper uses. I like it because the binary stays small and there is nothing to license.

**Card visibility.** In an early version the dog would stand directly on top of the card he just dug up. This was a problem because his 32×32 sprite completely covered the 36×36 card. The fix was simple: instead of stopping at the mound center, the dog approaches from the side—left or right depending on where he started—so the card is always fully in view while he digs and while it is revealed.

## The Stack

- **Go + Ebitengine v2** for game logic and rendering
- **WebAssembly** as the compilation target
- **Cloudflare Pages** for hosting at `digdog.jbirdkerr.net`
- **GitHub Actions** for CI/CD, path-filtered to only trigger on changes in `apps/digdog/`

The game is entirely static. No server, no database, no analytics. The binary is around 15MB uncompressed, which sounds large for a card game until you realize Cloudflare serves it with Brotli or gzip and the effective transfer size is closer to 4–5MB. Not great for 2G but fine for the intended audience of people who already know this site exists.

## What Is Left

The game works. You can play a full round, win, lose moves to mismatches, and restart. But there is a gap between "works" and "finished":

- **Levels.** Right now it is always 4×3. The plan is to grow the board each round—4×3, then 4×4, then 5×4—so a session has a natural arc.
- **The sniff power-up** is wired up but I have not decided on the cost. One use means one fewer dig on the current board? A limited pool that does not refill? It needs playtesting.
- **Keyboard navigation** is fully implemented (arrow keys move a focus cursor, Space or Enter digs), which matters to me because this site tries to be accessible to everyone, not just people with a mouse.
- **Stats.** I have the `metrics-worker` just sitting there looking pretty. I'm sure I could add trackable stats to the game once we have auth implemented across the site.

The code lives at [`apps/digdog/`](https://github.com/jbirdkerr/jbirdkerr-net/tree/main/apps/digdog) in the same monorepo as the blog and the snoot booper. Hank the Poodle Dog's empire continues to expand!
