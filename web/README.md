# StreamISO web page

The library and the player pages, in TypeScript with [Preact](https://preactjs.com). [Vite](https://vite.dev) bundles
them into static files that the catalog service serves: `index.html` at `/`, `player.html` at `/player`, and the rest
under `/static/`. The Android app shows these same pages. The TV apps (Samsung, LG) have their own.

It is built inside the catalog's Docker image (see [`../catalog/Dockerfile`](../catalog/Dockerfile)), so running
StreamISO needs nothing but Docker.

## Layout

| Folder | What is in it |
|---|---|
| `src/shared` | The API client, the types of the API's answers ([`../docs/API.md`](../docs/API.md)), time and size formatting |
| `src/library` | The library page |
| `src/player` | The player page |
| `public` | Copied as is: icons, logo, the web app manifest |

**Library.** The rules are in `catalog.ts`, with no DOM: seasons and films on several discs (read as the catalog
reads them, see `catalog/internal/domain/titles.go`), sorting, filtering, genre tags, and how the library is laid out
in sections. The components draw it: `Library.tsx` holds the state (the discs, the filters, the open windows) and the
keyboard and remote handling, and `Header`, `Cards`, `SeasonDialog`, `Editor` and `PosterPicker` are the parts.

**Player.** `controller.ts` holds the logic, apart from how it looks: it polls the player's status, sends the commands,
talks to the Neko page in the iframe (`neko/neko-inject.html`) and decides when the controls show. `Player.tsx` draws
its state.

The pages keep the element ids and classes the stylesheets (`library.css`, `player.css`) expect, and the Android app
relies on the routes `/` and `/player` and on `window.StreamIsoAndroid.changeServer()`.

## Commands

With Docker, from this folder (nothing to install; `node_modules` lives in a Docker volume):

```sh
docker run --rm -v "$PWD:/w" -v streamiso-web-nm:/w/node_modules -w /w node:22-alpine sh -c "npm ci && npm run build"
```

`npm run build` type-checks (`tsc`), runs the tests (`vitest`, for the rules in `catalog.ts`) and bundles into
`dist/`. `npm test` only runs the tests. With Node installed, `npm run dev` serves the pages with live reload, and
passes `/api` to a running catalog (`STREAMISO_URL`, `http://localhost:8080` by default).
