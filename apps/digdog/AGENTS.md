# AGENTS.md (apps/digdog)

DigDog is a pixel-art card-matching game: a dog digs up cards and matches pairs. Go + Ebiten v2, compiled to WebAssembly. It lives in a monorepo; **only change files inside this directory.**

## Files

- `main.go`: dog, animations, input, game loop.
- `cards.go`: Board rules (Pick, Reveal, Update) and placeholder drawing.
- `assets/dog.png`: sprite sheet, 128x160, 32x32 frames. Rows: idle 4, dig 4, sniff 3, happy 3, sad 2.
- `web/`: HTML page. The built `digdog.wasm` and `wasm_exec.js` are generated; never commit them.

## Commands

- `./check.sh`: run after EVERY change. It must print `OK`.
- `./build-wasm.sh`: builds `web/digdog.wasm`.
- Do not rely on `go run .`. Native Ebiten builds need graphics libraries this machine may lack.

## Rules

1. Read only the files the task names. Make the smallest change that works.
2. For existing files use the edit tool. Never rewrite a whole file with write.
3. Ebiten here is v2.10.5, newer than your training data. Before using any Ebiten function, run `go doc github.com/hajimehoshi/ebiten/v2 <Name>`. Do not guess APIs.
4. Standard library and Ebiten only. No new dependencies.
5. No network calls, analytics, or trackers. Audio never autoplays.
6. Logical screen is 320x180. Use nearest-neighbor filtering. No smoothing.
7. Never touch secrets, DNS, deployment config, or git history.
8. When finished: run `./check.sh`, list the files you changed, and say what you could not test (anything visual needs a human in a browser).
