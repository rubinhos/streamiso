# StreamISO catalog

The catalog service: it scans the media folders, serves the web page and the HTTP API
([`../docs/API.md`](../docs/API.md)), keeps the user's edits, resume positions and covers in `data/`, looks discs up on
TMDB and MusicBrainz, and drives the player (VLC inside the Neko container). It is written in Go with the standard
library only, and runs as the `catalog` service of [`../docker-compose.yml`](../docker-compose.yml).

The first version was written in Python. This one gives the same answers and reads and writes the same files, so
nothing changed for the web page, the TV apps or the Android app. The Python code is in the repository's history.

## Architecture

Hexagonal ("ports and adapters"): the rules and the use cases in the middle know nothing about files, HTTP, VLC or
TMDB; they talk to the outside world through interfaces (the ports), and the adapters implement them. Dependencies
only point inwards.

```
                         ┌──────────────────────── internal/httpapi ───────────────────────┐
  web page, TV apps ───▶ │  routes, JSON in and out, CORS, error pages                     │
                         └──────────────────────────────┬──────────────────────────────────┘
                                                        ▼
                         ┌────────────────────────── internal/app ─────────────────────────┐
                         │  Catalog     the last scan of the media folders                  │
                         │  Library     the discs as the user sees them, edits, covers      │
                         │  Metadata    TMDB and MusicBrainz lookups, posters, genre names  │
                         │  Playback    open, stop, status, commands, resume, plain video   │
                         └───────────────┬──────────────────────────────┬──────────────────┘
                                         ▼                              ▼
                         ┌── internal/domain ──┐        ┌──────── internal/ports ────────┐
                         │  Disc, titles,      │        │  MediaScanner, Library,        │
                         │  seasons, genres,   │        │  Metadata, Resume, Covers,     │
                         │  edits, CD tracks,  │        │  PlayerState, Player, MovieDB, │
                         │  placeholder cover  │        │  MusicDB, PlainVideo           │
                         └─────────────────────┘        └───────────────▲────────────────┘
                                                                        │ implemented by
                         ┌──────────────────────── internal/adapters ───┴──────────────────┐
                         │  fsstore      library.json, meta/, resume.json, covers, state     │
                         │  media        reads DVD_DIR (ISO) and CD_DIR (BIN/CUE)            │
                         │  vlc          VLC's remote-control interface                      │
                         │  tmdb         The Movie Database's API                            │
                         │  musicbrainz  MusicBrainz and the Cover Art Archive               │
                         │  neko         the plain video (HLS) through Neko's broadcast      │
                         └──────────────────────────────────────────────────────────────────┘
```

| Folder | What is in it |
|---|---|
| `cmd/catalog` | `main.go`, the only place that knows every piece: it builds the adapters and hands them to the use cases |
| `internal/config` | The settings, read once from the environment (`.env` through `docker-compose.yml`) |
| `internal/domain` | The rules, with no I/O: titles from file names, seasons and discs, genre tags, valid edits, CD tracks, the placeholder cover |
| `internal/compat` | Text and JSON rules the API's answers and the stored files depend on (strip, title case, truthiness, rounding half to even, text wrapping), kept from the first version |
| `internal/ports` | The interfaces the use cases need |
| `internal/app` | The use cases. Their errors carry the API's status and message (`app.Failure`) |
| `internal/adapters` | The outside world, one package per thing |
| `internal/httpapi` | The routes: each handler reads the request, calls a use case and writes its answer |
| `static` | The web page (plain HTML, CSS and JavaScript) |

A new outside service (another metadata source, another player) is a new adapter behind an existing port, or a new
port; the use cases and the API stay as they are.

## Building and running

It builds and runs with the rest of StreamISO, from the repository root:

```sh
docker compose up -d --build
```

## Tests

Everything runs in Docker, with nothing to install. From this folder:

```sh
docker run --rm -v "$PWD:/src" -w /src golang:1.24-alpine sh tools/check.sh
```

It formats the imports, then runs `go vet` and `go test`. [`internal/golden`](internal/golden) checks the rules
(titles, seasons, search queries, placeholder covers, edits, rounding, text wrapping) against
[`testdata/golden.json`](testdata/golden.json): the answers the first version gave for the same inputs, which the
service must keep giving.
