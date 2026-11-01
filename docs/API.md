# StreamISO HTTP API

The catalog service (`catalog/app.py`, port `CATALOG_PORT`, 8080 by default) serves both the web page and
a small JSON API. The web page uses nothing else, so any other client (a TV app, a phone remote, a script)
can do the same. Everything below is relative to `http://<server>:8080`.

**There is no login.** Anyone who can reach the port can play, stop and edit. Keep it on a trusted network
(or behind a VPN such as Tailscale).

Calling the API from another origin (for example an app that runs on a TV) needs CORS, which is **off by
default**. Set `CORS_ORIGIN` in `.env` to a comma-separated list of origins, or `*`. A packaged app that runs
from a `file://` URL sends the origin `null`. Only paths under `/api/` get the CORS headers.

## The playback flow

1. `GET /api/discs` to list the library.
2. `POST /api/play/<id>` to open a disc. The state file changes, and the player container starts VLC with the
   disc **paused**, so nothing is lost while the client connects.
3. `GET /api/player` to learn the video stream URL (and, for audio CDs, the track list).
4. Show the stream (an `<iframe>` with that URL, see [The video stream](#the-video-stream)).
5. Once the video is connected, send `{"action": "resume_apply"}` if the disc has something to resume, then
   `{"action": "start"}`. Both are safe to send more than once.
6. Poll `GET /api/player/status` about once per second for the position, and send commands to
   `POST /api/player/cmd`.
7. `POST /api/stop` when leaving the player (this closes VLC and clears the screen).

## Library

### `GET /api/discs`

```json
{
  "discs": [ { "...": "see below" } ],
  "genres": ["Animation", "Comedy"],
  "series": ["Some Series"],
  "media_dir": "/media/dvds and /media/cds",
  "tmdb": true,
  "playing": "3dca5f54c298"
}
```

`playing` is the id of the disc that is open right now, or `null`.

A disc:

| Field | Meaning |
|---|---|
| `id` | Stable id (12 hex characters, from the file path) |
| `kind` | `"dvd"` or `"cd"` |
| `title`, `year`, `genres`, `synopsis` | Effective values (your edits over the automatic ones) |
| `artist`, `track_count`, `duration` | Audio CDs only (`duration` in seconds) |
| `watched` | Watched (DVD) or listened (CD) flag |
| `resume` | `null`, or where it was left: `{ "time": seconds, "pct": 0-100 or null }`. For CDs it also has `track` (1-based) and `track_time` (seconds into that track) |
| `series`, `season`, `disc` | Grouping fields |
| `rating` | TMDB rating when known |
| `label`, `size`, `rel`, `added` | Volume label, bytes, path relative to the media folder, mtime |
| `cover_v` | Cache-busting value for the cover URL |
| `custom_cover` | `true` when the cover was uploaded by hand |
| `custom`, `auto` | The raw user edits and the automatic values behind the effective ones |

### `GET /api/cover/<id>`

The cover image (JPEG, PNG or WebP). Without a real cover you get a generated SVG placeholder (square for
CDs). Use `?v=<cover_v>` so a changed cover is not served from the cache. Images load across origins without
CORS, so `<img>` works from any client.

### Editing

| Call | Body | Effect |
|---|---|---|
| `PUT /api/disc/<id>/meta` | `{title, year, genres[], synopsis, series, season, disc, watched}` | Replaces all edits. Empty fields fall back to the automatic value. `400` on invalid values |
| `POST /api/disc/<id>/watched` | `{"watched": true}` | Sets or clears the watched/listened flag |
| `POST /api/disc/<id>/lookup` | `{"query": "optional search text"}` | Searches TMDB (DVD, needs `TMDB_API_KEY`) or MusicBrainz (CD) again. Returns `{found, cover, disc}` |
| `POST /api/disc/<id>/cover` | multipart form, file field `cover` (JPG, PNG, WebP, up to 8 MB) | Sets a custom cover |
| `DELETE /api/disc/<id>/cover` | none | Removes the custom cover |
| `POST /api/rescan` | none | Rescans the media folders. Returns `{count}` |

## Playing

### `POST /api/play/<id>`

Body (optional): `{"resume": true}` to continue where it was left (5 seconds earlier). Without it, the disc
starts over and any saved position is cleared.

```json
{ "player_url": "/player", "title": "Disc title", "kind": "dvd" }
```

`player_url` is the built-in web player page; another client can ignore it.

### `GET /api/player`

What is open now:

```json
{
  "url": "http://192.168.0.10:8081/?usr=tv&pwd=neko&cast=1",
  "title": "Disc title",
  "id": "3dca5f54c298",
  "kind": "dvd",
  "resume": true,
  "resume_at": 2535
}
```

Audio CDs add `artist`, `cover` and `tracks` (`[{n, title, artist, start, length}]`, seconds). Nothing open:
`title` and `id` are `null`.

`url` is built from the `Host` the request used, so ask the server by the same address the video will be
reached at (or set `NEKO_PUBLIC_URL`). It contains the player password; it is meant for the viewer.

### `GET /api/player/status`

Poll it about once a second. VLC takes a few seconds to start after `play`, during which you get
`{"state": "offline"}`. With nothing open: `{"state": "idle"}`.

DVD:

```json
{
  "state": "playing",
  "time": 312, "length": 6300,
  "volume": 80,
  "chapter": 4, "title": 1,
  "audio": [ { "id": 1, "name": "English" } ],
  "subtitles": [ { "id": -1, "name": "Disable" }, { "id": 2, "name": "English" } ]
}
```

`state` is VLC's own: `playing`, `paused`, `stopped`, `idle`, and so on. The subtitle entry with id `-1` means
"off".

Audio CD (one continuous stream split into tracks):

```json
{ "state": "playing", "kind": "cd", "volume": 80, "track": 2, "tracks": 18,
  "time": 41, "length": 127, "abs": 603 }
```

`track` is a zero-based index, `time` and `length` are relative to that track, `abs` is the position in the
whole disc.

Reading the status also saves the resume position for the disc, so a client that polls is what makes "continue
where you stopped" work.

### `POST /api/player/cmd`

Body: `{"action": "...", "value": ...}`. Returns `{"ok": true}`, or `{"error": "..."}` with `400` (bad input),
`409` (nothing is playing) or `503` (VLC is not ready yet, try again).

| `action` | `value` | Effect |
|---|---|---|
| `resume_apply` | none | Jump to the saved position (once). Send before `start` |
| `start` | none | Start a disc that was opened paused (once) |
| `toggle` | none | Pause or play |
| `seek` | seconds, 0-86400 | Seek. For CDs the seconds count from the start of the current track |
| `chapter_next`, `chapter_prev` | none | DVD chapters. For CDs these mean next and previous track (previous restarts the track when it is past 3 seconds) |
| `track_next`, `track_prev` | none | CD only, same as above |
| `track` | zero-based index | CD only: play that track |
| `volume` | 0-100 | Volume |
| `audio` | id from `status.audio` | DVD audio track |
| `subtitle` | id from `status.subtitles`, `-1` to hide | DVD subtitles |
| `menu` | none | DVD main menu |
| `nav` | `up`, `down`, `left`, `right`, `activate` | Move in a DVD menu (VLC remote interface). Useful when the video has no keyboard focus |

### `POST /api/stop`

Closes VLC and clears the screen. Returns `{"ok": true}`.

### `GET /healthz`

`{"ok": true, "version": "0.6.0"}` for health checks.

## The video stream

The picture and sound are not part of this API. They are a WebRTC stream served by the player container
(Neko). The simplest way to show them is an `<iframe src="<url from /api/player>">`; the URL already asks for
the minimal "cast" view with no Neko interface. A custom client could speak Neko's own protocol instead, but
nothing in StreamISO depends on that.

The Neko page is patched with a small script (`neko/neko-inject.html`) that talks to the page that embeds it
with `postMessage`. Every message has `streamiso: true`:

| From the stream page | Meaning |
|---|---|
| `{type: "video-ready"}` | The first video data arrived. Hide your loading cover |
| `{type: "audio-state", ok: boolean}` | `ok` is `true` when the stream is audible. When it stays `false` the browser blocked autoplay with sound and needs a tap |
| `{type: "activity"}` | The pointer or touch moved over the video (to show your controls) |
| `{type: "key", action: "..."}` | A remote-control key: `toggle`, `forward`, `rewind`, `next`, `prev`, `stop`, `back`, or `nav-up`, `nav-down`, `nav-left`, `nav-right`, `nav-activate` (the last five only when the viewer has no control of the desktop) |

| To the stream page | Meaning |
|---|---|
| `{streamiso: true, type: "start"}` | Send it from a real user gesture (a tap or key press). The stream page takes control of the desktop, focuses its input and unmutes. This is what makes the DVD menu answer to the keyboard without clicking the picture first |

The stream page keeps VLC's own shortcuts out of reach: only the arrow keys, Enter, Space and the media keys
go on to VLC, and right and middle clicks are ignored. Drive everything else through `/api/player/cmd`.
