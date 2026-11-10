# StreamISO catalog in Go

A copy of the catalog service ([`../catalog`](../catalog), Python and Flask) written in Go. It serves the same web
page and the same HTTP API ([`../docs/API.md`](../docs/API.md)), and reads and writes the same files in `data/`, so
the web page, the TV app and the Android app work with it unchanged. The player (Neko with VLC) is the same.

Nothing in the main application uses this folder yet. Both versions can run from the same data, one at a time.

| | Python | Go |
|---|---|---|
| Image | ~200 MB | ~25 MB |
| Memory (catalog of 50 discs) | ~45 MB | ~5 MB |
| Dependencies | Flask, gunicorn | none (standard library only) |

## Running it

From this folder, with the settings of the main one:

```sh
docker compose -f ../docker-compose.yml down        # the two use the same ports
docker compose --env-file ../.env up -d --build
```

It uses the same `.env`, media folders, `../data` and `../state`, so the library, edits, covers and resume positions
are the ones of the Python version. To go back:

```sh
docker compose --env-file ../.env down
docker compose -f ../docker-compose.yml up -d
```

[`docker-compose.yml`](docker-compose.yml) is [`../docker-compose.yml`](../docker-compose.yml) with the paths
pointing one folder up, the catalog built from here, and its own container names. A `COMPOSE_FILE` line in `.env`
(the VP8 switch) works here too: [`docker-compose.vp8.yml`](docker-compose.vp8.yml) is a copy.

`static/` is a copy of `../catalog/static`. A change to the web page has to be copied here too.

## Code

| File | Python | What it does |
|---|---|---|
| `main.go` | `app.py` | Start-up and the catalog (rescanned every 30 s) |
| `handlers.go` | `app.py` | Routes, the disc view, edits, playback, CORS |
| `scan.go` | `app.py` | Reading the media folders and the ISO volume descriptor |
| `titles.go` | `app.py` | Titles from file names, season and disc numbers, localized titles |
| `tmdb.go` | `app.py` | TMDB lookups, posters, the background lookup |
| `store.go` | `app.py` | Settings, `library.json`, `meta/`, `resume.json`, covers |
| `cue.go` | `cdimage.py` | Audio CD cue sheets |
| `musicbrainz.go` | `musicbrainz.py` | MusicBrainz and the Cover Art Archive |
| `vlcrc.go` | `vlcrc.py` | VLC's remote-control interface |
| `pyutil.go` | | Python's own rules where the answer depends on them: `str.title()`, `strip()`, `lower()`, truthiness, `round()` (half to even), `textwrap.wrap` (placeholder covers) |

Go's regular expressions have no lookahead, so the season and disc patterns (`S01 D2`, `Disc 2`, `Disco 1 de 2`)
are matched by hand in `titles.go`, with the same backtracking as Python's.

## Tests

Two checks against the Python version, both run with Docker (no local Go or Python needed). From the repository root:

**Functions** (titles, seasons, search queries, placeholder covers, edits): [`tools/golden.py`](tools/golden.py)
writes what the Python code answers to [`testdata/golden.json`](testdata/golden.json), and `go test` compares.

```sh
docker build -t streamiso-catalog catalog
docker run --rm -v "$PWD:/w" streamiso-catalog python -I /w/go/tools/golden.py
docker run --rm -v "$PWD/go:/src" -w /src golang:1.24-alpine go test ./...
```

Passing a saved `GET /api/discs` to `golden.py` checks the titles of your own library too (keep that `golden.json`
to yourself).

**The whole service**: [`tools/difftest.py`](tools/difftest.py) runs both versions side by side, each with a fake
TMDB, MusicBrainz and VLC, on test discs and CDs. It sends both about 200 requests (listing, covers, edits, lookups,
posters, uploads, playback and VLC commands, CORS, errors) and compares the answers, the files they write, the
commands they send to VLC and the requests they make to TMDB and MusicBrainz.

```sh
docker run --rm -v "$PWD/go:/src" -w /src -e CGO_ENABLED=0 golang:1.24-alpine go build -o tools/catalog-go .
docker run --rm -v "$PWD:/w" streamiso-catalog python -I -u /w/go/tools/difftest.py
```

It ends with `N checks, 0 differences`.

## Known differences

None in what clients see. What differs is only in form:

- The files in `data/` are written with their keys sorted and, in `meta/`, without spaces. API answers send
  non-ASCII text as UTF-8 instead of `\u` escapes. The values are the same, and each version reads the other's files.
- Network error messages (`"Lookup failed: ..."`) are Go's wording.
- Digits other than 0-9 (Arabic, Devanagari) are not read as season or disc numbers.
