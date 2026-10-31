#!/usr/bin/env python3
"""Streamiso - catalog service.

Scans a folder for DVD .iso images, reads each volume label, builds the library
with covers (custom cover > image next to the ISO > TMDB > generated placeholder)
and stores the user's edits (title, year, genres, synopsis, series/season/disc,
watched) in DATA_DIR/library.json.

Edits are tied to a fingerprint of the ISO (not to its path), so renaming or
moving a file does not lose them.

To play a disc, the catalog writes the ISO path to a state file that the Neko
container (running VLC) watches. The player screen is streamed by Neko over WebRTC.
"""
import hashlib
import json
import os
import re
import textwrap
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter
from pathlib import Path
from xml.sax.saxutils import escape

from flask import Flask, Response, abort, jsonify, request, send_file, send_from_directory

import musicbrainz
from cdimage import CueError, locate, parse_cue, track_spans
from vlcrc import VlcError, VlcRc

VERSION = "0.6.0"

# Where to look: DVD images (.iso) in DVD_DIR, audio CD images (.cue + .bin) in CD_DIR.
# MEDIA_DIR is the older single-folder setting and the fallback for both.
_media = os.environ.get("MEDIA_DIR", "")
DVD_DIR = Path(os.environ.get("DVD_DIR") or _media or "/media/dvds")
CD_DIR = Path(os.environ.get("CD_DIR") or _media or "/media/cds")
DATA_DIR = Path(os.environ.get("DATA_DIR", "/data"))
STATE_FILE = Path(os.environ.get("STATE_FILE", "/state/current"))
VLC_RC = VlcRc(os.environ.get("VLC_RC_HOST", "neko"), int(os.environ.get("VLC_RC_PORT", "4212")))

NEKO_PORT = os.environ.get("NEKO_PORT", "8081")
NEKO_PUBLIC_URL = os.environ.get("NEKO_PUBLIC_URL", "").rstrip("/")
NEKO_USER = os.environ.get("NEKO_USER", "tv")
NEKO_PASSWORD = os.environ.get("NEKO_PASSWORD", "neko")

TMDB_API_KEY = os.environ.get("TMDB_API_KEY", "").strip()
TMDB_LANGUAGE = os.environ.get("TMDB_LANGUAGE", "en-US")
TMDB_API_URL = os.environ.get("TMDB_API_URL", "https://api.themoviedb.org/3").rstrip("/")  # overridable for tests
TMDB_IMAGE_URL = os.environ.get("TMDB_IMAGE_URL", "https://image.tmdb.org/t/p/w500").rstrip("/")

MB_DELAY = 1.1  # MusicBrainz allows about one request per second

RESCAN_TTL = 30  # seconds between automatic rescans
COVER_DIR = DATA_DIR / "covers"                # covers downloaded from TMDB
CUSTOM_COVER_DIR = DATA_DIR / "covers_custom"  # covers uploaded by the user
META_DIR = DATA_DIR / "meta"                   # TMDB cache
LIB_FILE = DATA_DIR / "library.json"           # user edits
MAX_COVER_BYTES = 8 * 1024 * 1024

IMAGE_EXTS = (".jpg", ".jpeg", ".png", ".webp")
CUSTOM_EXTS = (".jpg", ".png", ".webp")
# Folder-level cover names (used only when the folder holds a single ISO).
FOLDER_COVER_NAMES = ("cover", "folder", "poster", "front")
# File names that say nothing about the disc, so the folder name / volume label is used instead.
_cue_warned: set[tuple[str, int]] = set()
GENERIC_TITLES = {"videots", "dvd", "dvdvideo", "disc", "disco", "movie", "filme", "video"}

app = Flask(__name__, static_folder="static", static_url_path="/static")
app.config["MAX_CONTENT_LENGTH"] = MAX_COVER_BYTES + 256 * 1024

_lock = threading.Lock()
_lib_lock = threading.Lock()
_catalog: dict[str, dict] = {}
_scanned_at = 0.0
_enrich_lock = threading.Lock()
_genre_cache: dict[str, dict[int, str]] = {}


# --------------------------------------------------------------------------
# Reading the ISOs
# --------------------------------------------------------------------------
def read_pvd(path: Path) -> bytes:
    """Read the ISO 9660 primary volume descriptor (sector 16, 2048 bytes); empty if not an ISO."""
    try:
        with open(path, "rb") as f:
            f.seek(0x8000)
            data = f.read(2048)
    except OSError:
        return b""
    return data if len(data) == 2048 and data[1:6] == b"CD001" else b""


def clean_title(raw: str) -> tuple[str, str | None]:
    """Turn a file name / volume label into a readable title; returns (title, year)."""
    year = None
    m = re.search(r"\(((?:19|20)\d{2})\)", raw)
    if m:
        year = m.group(1)
    s = re.sub(r"\((?:19|20)\d{2}\)|\[[^\]]*\]", " ", raw)
    s = re.sub(r"[_.]+", " ", s)
    s = re.sub(r"\s+", " ", s).strip(" -")
    if s and s.upper() == s:
        s = s.title()
    return s, year


def is_generic(title: str) -> bool:
    return re.sub(r"[^a-z]", "", title.lower()) in GENERIC_TITLES or not title


def find_user_cover(iso: Path, isos_in_dir: int) -> Path | None:
    for ext in IMAGE_EXTS:
        cand = iso.with_suffix(ext)
        if cand.is_file():
            return cand
        cand = iso.with_name(iso.stem + ".cover" + ext)
        if cand.is_file():
            return cand
    if isos_in_dir == 1:  # a folder cover only makes sense when the folder has a single disc
        for name in FOLDER_COVER_NAMES:
            for ext in IMAGE_EXTS:
                cand = iso.parent / (name + ext)
                if cand.is_file():
                    return cand
    return None


def scan_cue(p: Path, per_dir: Counter) -> dict | None:
    """One audio CD image (BIN/CUE) as a disc entry, or None if the sheet is not usable."""
    try:
        st = p.stat()
        info = parse_cue(p)
        bin_path = info["bin"].resolve()
        bin_path.relative_to(CD_DIR.resolve())  # the image must live inside the CD folder
        bin_size = bin_path.stat().st_size
    except (CueError, OSError, ValueError) as exc:
        key = (str(p), int(p.stat().st_mtime) if p.exists() else 0)
        if key not in _cue_warned:
            _cue_warned.add(key)
            app.logger.warning("Skipping %s: %s", p, exc)
        return None
    rel = p.relative_to(CD_DIR).as_posix()
    title, year = clean_title(p.stem)
    if info["album"]:
        title = info["album"]
    year = info["year"] or year
    spans = track_spans(info)
    fmt = info["byte_order"] if info["raw"] else "file"
    state = f"cd-{fmt}:{bin_path}" if info["raw"] else f"file:{bin_path}"
    return {
        "id": hashlib.sha1(rel.encode()).hexdigest()[:12],
        "fp": hashlib.sha1(info["text"].encode("utf-8", "ignore") + str(bin_size).encode()).hexdigest()[:16],
        "kind": "cd",
        "path": str(p),
        "state": state,
        "rel": rel,
        "title": title,
        "artist": info["artist"],
        "year": year,
        "label": "",
        "size": bin_size,
        "added": int(st.st_mtime),
        "raw": info["raw"],
        "duration": info["duration"],
        "tracks": spans,
        "genres_auto": [info["genre"]] if info["genre"] else [],
        "user_cover": str(c) if (c := find_user_cover(p, per_dir[p.parent])) else None,
    }


def scan() -> dict[str, dict]:
    """Scan DVD_DIR for ISOs and CD_DIR for cue sheets and return {id: disc}."""
    isos = sorted(
        p for p in DVD_DIR.rglob("*") if p.suffix.lower() == ".iso" and p.is_file()
    ) if DVD_DIR.is_dir() else []
    cues = sorted(
        p for p in CD_DIR.rglob("*") if p.suffix.lower() == ".cue" and p.is_file()
    ) if CD_DIR.is_dir() else []
    per_dir = Counter(p.parent for p in isos + cues)
    found: dict[str, dict] = {}
    for p in cues:
        disc = scan_cue(p, per_dir)
        if disc:
            found[disc["id"]] = disc
    for p in isos:
        rel = p.relative_to(DVD_DIR).as_posix()
        disc_id = hashlib.sha1(rel.encode()).hexdigest()[:12]
        pvd = read_pvd(p)
        label = pvd[40:72].decode("ascii", "ignore").strip() if pvd else ""
        try:
            st = p.stat()
            size, added = st.st_size, int(st.st_mtime)
        except OSError:
            size, added = 0, 0

        # Fingerprint: the volume descriptor (label, creation date, volume size...)
        # plus the file size. Survives renaming/moving the ISO.
        fp_src = pvd + str(size).encode() if pvd else (rel + str(size)).encode()
        fp = hashlib.sha1(fp_src).hexdigest()[:16]

        title, year = clean_title(p.stem)
        if is_generic(title):
            # generic file name: try the folder name (if it holds a single ISO), then the volume label
            if per_dir[p.parent] == 1 and p.parent != DVD_DIR:
                ftitle, fyear = clean_title(p.parent.name)
                if ftitle and not is_generic(ftitle):
                    title, year = ftitle, fyear or year
            if is_generic(title):
                ltitle, lyear = clean_title(label)
                if ltitle and not is_generic(ltitle):
                    title, year = ltitle, year or lyear
                elif not title:
                    title = p.stem
        found[disc_id] = {
            "id": disc_id,
            "fp": fp,
            "kind": "dvd",
            "path": str(p),
            "state": str(p),
            "rel": rel,
            "title": title,
            "year": year,
            "label": label,
            "size": size,
            "added": added,
            "user_cover": str(c) if (c := find_user_cover(p, per_dir[p.parent])) else None,
        }
    return found


def get_catalog(force: bool = False) -> dict[str, dict]:
    global _catalog, _scanned_at
    with _lock:
        if force or time.time() - _scanned_at > RESCAN_TTL:
            _catalog = scan()
            _scanned_at = time.time()
            # DVDs are looked up on TMDB (needs a key), CDs on MusicBrainz (no key)
            threading.Thread(target=enrich_all, daemon=True).start()
        return _catalog


def get_disc(disc_id: str) -> dict:
    disc = get_catalog().get(disc_id)
    if not disc:
        abort(404)
    return disc


# --------------------------------------------------------------------------
# Metadata and covers (TMDB, optional)
# --------------------------------------------------------------------------
def read_meta(disc_id: str) -> dict:
    try:
        return json.loads((META_DIR / f"{disc_id}.json").read_text("utf-8"))
    except (OSError, ValueError):
        return {}


def tmdb_get(path: str, **params) -> dict:
    params["api_key"] = TMDB_API_KEY
    url = f"{TMDB_API_URL}{path}?{urllib.parse.urlencode(params)}"
    req = urllib.request.Request(url, headers={"User-Agent": f"streamiso/{VERSION}"})
    with urllib.request.urlopen(req, timeout=15) as resp:
        return json.load(resp)


def tmdb_genre_map(kind: str) -> dict[int, str]:
    if kind not in _genre_cache:
        data = tmdb_get(f"/genre/{kind}/list", language=TMDB_LANGUAGE)
        _genre_cache[kind] = {g["id"]: g["name"] for g in data.get("genres", [])}
    return _genre_cache[kind]


def _words(text: str) -> list[str]:
    return re.sub(r"[^\w]+", " ", text.lower()).split()


def title_variants(title: str) -> list[tuple[str, bool]]:
    """Search queries to try for a title, most specific first: [(query, strict)].

    Long file names like "Show Name Special Episode Title" often find nothing as a whole, but the
    series name alone does. Shorter queries are "strict": a result must contain all their words.
    """
    out: list[tuple[str, bool]] = []

    def add(query: str, strict: bool) -> None:
        query = re.sub(r"\s+", " ", query).strip()
        if query and query.lower() not in (q.lower() for q, _ in out):
            out.append((query, strict))

    add(title, False)
    plain = " ".join(_words(title))
    add(plain, False)
    for sep in (":", " - ", " \u2013 ", " \u2014 "):
        if sep in title:
            head, _, tail = title.partition(sep)
            add(head, True)
            add(tail, True)
    words = plain.split()
    for n in range(len(words) - 1, 1, -1):  # drop trailing words, keep at least two
        add(" ".join(words[:n]), True)
    return out[:7]


def tmdb_lookup(title: str, year: str | None) -> dict | None:
    for query, strict in title_variants(title):
        want = set(_words(query))
        for kind in ("movie", "tv"):
            params = {"query": query, "language": TMDB_LANGUAGE, "include_adult": "false"}
            if year and kind == "movie":
                params["year"] = year
            for r in tmdb_get(f"/search/{kind}", **params).get("results", []):
                if not r.get("poster_path"):
                    continue
                names = " ".join(filter(None, [r.get("title"), r.get("name"), r.get("original_title"),
                                               r.get("original_name")]))
                if strict and not want <= set(_words(names)):
                    continue
                gmap = tmdb_genre_map(kind)
                return {
                    "poster_path": r["poster_path"],
                    "overview": r.get("overview", ""),
                    "tmdb_title": r.get("title") or r.get("name") or "",
                    "tmdb_year": (r.get("release_date") or r.get("first_air_date") or "")[:4],
                    "rating": r.get("vote_average"),
                    "genres": [gmap[i] for i in r.get("genre_ids", []) if i in gmap],
                    "matched": query,
                }
    return None


def _store_meta(disc_id: str, meta: dict) -> None:
    META_DIR.mkdir(parents=True, exist_ok=True)
    (META_DIR / f"{disc_id}.json").write_text(json.dumps(meta, ensure_ascii=False), "utf-8")


def lookup_dvd(disc: dict, query: str | None = None) -> bool:
    """TMDB lookup for a DVD: stores synopsis/genres/year and the poster. Raises on network errors."""
    title = query or load_library().get(disc["fp"], {}).get("title") or disc["title"]
    hit = tmdb_lookup(title, None if query else disc["year"])
    if hit is None:
        _store_meta(disc["id"], {"miss": True, "checked": int(time.time())})
        return False
    COVER_DIR.mkdir(parents=True, exist_ok=True)
    if not disc["user_cover"]:
        img = urllib.request.Request(
            f"{TMDB_IMAGE_URL}{hit['poster_path']}",
            headers={"User-Agent": f"streamiso/{VERSION}"},
        )
        try:
            with urllib.request.urlopen(img, timeout=20) as resp:
                (COVER_DIR / f"{disc['id']}.jpg").write_bytes(resp.read())
        except urllib.error.HTTPError as exc:  # the details are still worth keeping without the poster
            app.logger.warning("Poster download failed for %r: HTTP %s", disc["title"], exc.code)
    meta = {k: v for k, v in hit.items() if k != "poster_path"}
    meta["checked"] = int(time.time())
    _store_meta(disc["id"], meta)
    return True


def lookup_cd(disc: dict, query: str | None = None) -> bool:
    """MusicBrainz / Cover Art Archive lookup for an audio CD. Raises on network errors."""
    hit = musicbrainz.lookup_album(query or disc["title"], disc.get("artist", ""), VERSION)
    time.sleep(MB_DELAY)
    if hit is None:
        _store_meta(disc["id"], {"miss": True, "checked": int(time.time())})
        return False
    got_cover = bool(disc["user_cover"])
    if not got_cover:
        image = musicbrainz.fetch_cover(hit["mbid"], VERSION)
        if image:
            got_cover = True
            COVER_DIR.mkdir(parents=True, exist_ok=True)
            (COVER_DIR / f"{disc['id']}.jpg").write_bytes(image)
        time.sleep(0.3)
    _store_meta(disc["id"], {"source": "musicbrainz", "mbid": hit["mbid"], "year": hit["year"],
                             "genres": hit["genres"], "overview": "", "checked": int(time.time())})
    disc["_cover_found"] = got_cover
    return True


def enrich_all() -> None:
    """Look up covers/metadata for discs not looked up yet (background thread)."""
    if not _enrich_lock.acquire(blocking=False):
        return
    try:
        for disc in list(_catalog.values()):
            meta_path = META_DIR / f"{disc['id']}.json"
            is_cd = disc.get("kind") == "cd"
            if not is_cd and not TMDB_API_KEY:
                continue  # DVDs need a TMDB key
            if meta_path.exists():
                old = read_meta(disc["id"])
                if is_cd or old.get("miss") or "genres" in old:
                    continue  # already looked up (older DVD lookups without genres are redone)
            try:
                (lookup_cd if is_cd else lookup_dvd)(disc)
            except (urllib.error.URLError, OSError, ValueError, KeyError) as exc:
                app.logger.warning("Lookup failed for %r: %s", disc["title"], exc)
                time.sleep(2)  # offline / invalid key: try again on the next scan
            time.sleep(0.3)
    finally:
        _enrich_lock.release()


def placeholder_svg(title: str, year: int | str | None, square: bool = False) -> str:
    h = 400 if square else 600
    hue = int(hashlib.md5(title.encode()).hexdigest()[:4], 16) % 360
    lines = textwrap.wrap(title, 14)[:5] or ["DVD"]
    start_y = (250 if square else 330) - (len(lines) - 1) * 24
    cy = 90 if square else 140
    text = "".join(
        f'<text x="200" y="{start_y + i * 48}" text-anchor="middle" '
        f'font-family="Helvetica, Arial, sans-serif" font-size="38" font-weight="700" '
        f'fill="#fff">{escape(line)}</text>'
        for i, line in enumerate(lines)
    )
    year_txt = (
        f'<text x="200" y="{h - 60}" text-anchor="middle" font-family="Helvetica, Arial, sans-serif" '
        f'font-size="26" fill="#ffffffaa">{escape(str(year))}</text>'
        if year
        else ""
    )
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 {h}">'
        f'<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">'
        f'<stop offset="0" stop-color="hsl({hue},45%,32%)"/>'
        f'<stop offset="1" stop-color="hsl({(hue + 40) % 360},50%,14%)"/></linearGradient></defs>'
        f'<rect width="400" height="{h}" fill="url(#g)"/>'
        f'<circle cx="200" cy="{cy}" r="62" fill="none" stroke="#ffffff55" stroke-width="6"/>'
        f'<circle cx="200" cy="{cy}" r="14" fill="#ffffff55"/>'
        f"{text}{year_txt}</svg>"
    )


# --------------------------------------------------------------------------
# User edits (data/library.json) and custom covers
# --------------------------------------------------------------------------
def load_library() -> dict:
    try:
        data = json.loads(LIB_FILE.read_text("utf-8"))
        return data if isinstance(data, dict) else {}
    except (OSError, ValueError):
        return {}


def save_library(lib: dict) -> None:
    write_file(LIB_FILE, json.dumps(lib, ensure_ascii=False, indent=1))


def _int_or_none(value, lo: int, hi: int, what: str) -> int | None:
    if value is None or value == "":
        return None
    try:
        n = int(value)
    except (TypeError, ValueError):
        raise ValueError(f"{what}: not a valid number")
    if not lo <= n <= hi:
        raise ValueError(f"{what}: use a value between {lo} and {hi}")
    return n


def clean_meta(payload: dict) -> dict:
    """Validate/normalize the edits; empty fields are dropped (they fall back to the automatic value)."""
    out: dict = {}
    title = str(payload.get("title") or "").strip()
    if title:
        out["title"] = title[:200]
    year = _int_or_none(payload.get("year"), 1888, 2100, "Year")
    if year is not None:
        out["year"] = year

    genres = payload.get("genres") or []
    if isinstance(genres, str):
        genres = genres.split(",")
    if not isinstance(genres, list):
        genres = []
    seen: set[str] = set()
    clean: list[str] = []
    for g in genres:
        g = str(g).strip()[:40]
        if g and g.casefold() not in seen:
            seen.add(g.casefold())
            clean.append(g)
    if clean:
        out["genres"] = clean[:12]

    synopsis = str(payload.get("synopsis") or "").strip()
    if synopsis:
        out["synopsis"] = synopsis[:4000]
    series = str(payload.get("series") or "").strip()
    if series:
        out["series"] = series[:120]
    season = _int_or_none(payload.get("season"), 0, 999, "Season")
    if season is not None:
        out["season"] = season
    disc = _int_or_none(payload.get("disc"), 1, 999, "Disc")
    if disc is not None:
        out["disc"] = disc
    if payload.get("watched"):
        out["watched"] = True
    return out


def sniff_image(data: bytes) -> str | None:
    if data.startswith(b"\xff\xd8\xff"):
        return ".jpg"
    if data.startswith(b"\x89PNG\r\n\x1a\n"):
        return ".png"
    if data[:4] == b"RIFF" and data[8:12] == b"WEBP":
        return ".webp"
    return None


def find_custom_cover(fp: str) -> Path | None:
    for ext in CUSTOM_EXTS:
        p = CUSTOM_COVER_DIR / f"{fp}{ext}"
        if p.is_file():
            return p
    return None


def cover_version(disc: dict) -> int:
    """Changes whenever the cover changes; keeps the browser from serving a stale cached cover."""
    p = (
        find_custom_cover(disc["fp"])
        or (Path(disc["user_cover"]) if disc["user_cover"] else None)
        or COVER_DIR / f"{disc['id']}.jpg"
    )
    try:
        return int(p.stat().st_mtime)
    except OSError:
        return 0


def disc_view(d: dict, lib: dict) -> dict:
    """A disc as the UI sees it: user edits layered over the automatic values."""
    meta = read_meta(d["id"])
    ov = lib.get(d["fp"], {})
    if d["year"]:
        year_auto = int(d["year"])
    elif str(meta.get("tmdb_year") or meta.get("year") or "").isdigit():
        year_auto = int(meta.get("tmdb_year") or meta["year"])
    else:
        year_auto = None
    auto = {
        "title": d["title"],
        "year": year_auto,
        "genres": d.get("genres_auto") or meta.get("genres", []),
        "synopsis": meta.get("overview", ""),
    }
    return {
        "id": d["id"],
        "kind": d.get("kind", "dvd"),
        "artist": d.get("artist", ""),
        "track_count": len(d.get("tracks", [])),
        "duration": d.get("duration"),
        "title": ov.get("title") or auto["title"],
        "year": ov["year"] if ov.get("year") is not None else auto["year"],
        "genres": ov.get("genres") or auto["genres"],
        "synopsis": ov.get("synopsis") or auto["synopsis"],
        "watched": bool(ov.get("watched")),
        "resume": resume_view(d),
        "series": ov.get("series", ""),
        "season": ov.get("season"),
        "disc": ov.get("disc"),
        "rating": meta.get("rating"),
        "label": d["label"],
        "size": d["size"],
        "rel": d["rel"],
        "added": d["added"],
        "cover_v": cover_version(d),
        "custom_cover": find_custom_cover(d["fp"]) is not None,
        "custom": ov,
        "auto": auto,
    }


# --------------------------------------------------------------------------
# Playback state and commands (read by the launcher inside the Neko container)
# --------------------------------------------------------------------------
def write_file(path: Path, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(value, "utf-8")
    os.chmod(tmp, 0o644)
    os.replace(tmp, path)


def write_state(value: str) -> None:
    write_file(STATE_FILE, value)


# --------------------------------------------------------------------------
# Resume positions: where each disc was left, keyed by the disc fingerprint (data/resume.json)
RESUME_FILE = DATA_DIR / "resume.json"
_resume_lock = threading.Lock()
_resume_cache: dict | None = None


def get_resume() -> dict:
    global _resume_cache
    with _resume_lock:
        if _resume_cache is None:
            try:
                data = json.loads(RESUME_FILE.read_text("utf-8"))
                _resume_cache = data if isinstance(data, dict) else {}
            except (OSError, ValueError):
                _resume_cache = {}
        return dict(_resume_cache)


def _set_resume(fp: str, entry: dict | None) -> None:
    get_resume()
    with _resume_lock:
        if entry is None:
            if _resume_cache.pop(fp, None) is None:
                return
        else:
            _resume_cache[fp] = entry
        write_file(RESUME_FILE, json.dumps(_resume_cache, ensure_ascii=False, indent=1))


# What the player page still has to do for the disc that was just opened. VLC opens paused, so the
# first seconds are not lost before the browser's audio/video is connected.
_pending: dict = {}


def remember_position(disc: dict, pos: int | None, length: int | None, title: int | None) -> None:
    """Save the playback position (called from the status poll, about once per second)."""
    if _pending.get("state") == disc["state"] and (_pending.get("wait") or _pending.get("resume")):
        return  # still starting up: the position says nothing yet
    if pos is None:
        return
    is_cd = disc.get("kind") == "cd"
    if pos < (10 if is_cd else 30):
        return
    if not is_cd and (length or 0) < 300:
        return  # a menu or a short extra, nothing worth resuming
    fp = disc["fp"]
    if length and pos >= length - (5 if is_cd else max(60, length * 0.03)):
        _set_resume(fp, None)  # reached the end: next time starts over
        return
    last = get_resume().get(fp)
    if last and abs(last.get("time", -99) - pos) < 5:
        return
    _set_resume(fp, {"time": int(pos), "length": int(length) if length else None,
                     "title": int(title) if title else 0, "updated": int(time.time())})


def resume_view(disc: dict) -> dict | None:
    entry = get_resume().get(disc["fp"])
    if not entry:
        return None
    pos = int(entry.get("time", 0))
    length = entry.get("length") or (int(disc["duration"]) if disc.get("duration") else None)
    out = {"time": pos, "pct": round(100 * pos / length) if length else None}
    if disc.get("kind") == "cd":
        spans = disc["tracks"]
        idx = locate(spans, pos)
        out["track"] = idx + 1
        out["track_time"] = max(0, pos - int(spans[idx]["start"]))
    return out


def read_state() -> str:
    try:
        return STATE_FILE.read_text("utf-8").strip()
    except OSError:
        return ""


def neko_url() -> str:
    query = urllib.parse.urlencode({"usr": NEKO_USER, "pwd": NEKO_PASSWORD, "cast": "1"})
    if NEKO_PUBLIC_URL:
        return f"{NEKO_PUBLIC_URL}/?{query}"
    host = urllib.parse.urlsplit("//" + request.host).hostname or "localhost"
    if ":" in host:  # IPv6
        host = f"[{host}]"
    return f"http://{host}:{NEKO_PORT}/?{query}"


# --------------------------------------------------------------------------
# Routes
# --------------------------------------------------------------------------
@app.get("/")
def index():
    return send_from_directory(app.static_folder, "index.html")


@app.get("/favicon.ico")
def favicon():
    return send_from_directory(app.static_folder, "icons/favicon.ico", max_age=86400)


@app.get("/manifest.webmanifest")
def manifest():
    return send_from_directory(app.static_folder, "manifest.webmanifest", mimetype="application/manifest+json")


@app.get("/healthz")
def healthz():
    return {"ok": True, "version": VERSION}


@app.get("/api/discs")
def api_discs():
    lib = load_library()
    catalog = get_catalog()
    playing_path = read_state()
    discs = [disc_view(d, lib) for d in catalog.values()]
    return jsonify(
        {
            "discs": discs,
            "genres": sorted({g for d in discs for g in d["genres"]}, key=str.casefold),
            "series": sorted({d["series"] for d in discs if d["series"]}, key=str.casefold),
            "media_dir": str(DVD_DIR) if DVD_DIR == CD_DIR else f"{DVD_DIR} and {CD_DIR}",
            "tmdb": bool(TMDB_API_KEY),
            "playing": next((d["id"] for d in catalog.values() if d["state"] == playing_path), None),
        }
    )


@app.get("/api/cover/<disc_id>")
def api_cover(disc_id: str):
    disc = get_disc(disc_id)
    custom = find_custom_cover(disc["fp"])
    if custom:
        return send_file(custom, max_age=3600)
    if disc["user_cover"]:
        return send_file(disc["user_cover"], max_age=3600)
    cached = COVER_DIR / f"{disc_id}.jpg"
    if cached.is_file():
        return send_file(cached, mimetype="image/jpeg", max_age=86400)
    ov = load_library().get(disc["fp"], {})
    return Response(
        placeholder_svg(ov.get("title") or disc["title"], ov.get("year") or disc["year"],
                        square=disc.get("kind") == "cd"),
        mimetype="image/svg+xml",
        headers={"Cache-Control": "no-cache"},
    )


@app.put("/api/disc/<disc_id>/meta")
def api_meta_put(disc_id: str):
    """Replace all edits for the disc (empty fields fall back to the automatic value)."""
    disc = get_disc(disc_id)
    payload = request.get_json(silent=True)
    if not isinstance(payload, dict):
        return jsonify({"error": "Invalid JSON"}), 400
    try:
        clean = clean_meta(payload)
    except ValueError as exc:
        return jsonify({"error": str(exc)}), 400
    with _lib_lock:
        lib = load_library()
        if clean:
            lib[disc["fp"]] = clean
        else:
            lib.pop(disc["fp"], None)
        save_library(lib)
    return jsonify(disc_view(disc, lib))


@app.post("/api/disc/<disc_id>/watched")
def api_watched(disc_id: str):
    disc = get_disc(disc_id)
    payload = request.get_json(silent=True)
    watched = bool(payload.get("watched")) if isinstance(payload, dict) else False
    with _lib_lock:
        lib = load_library()
        rec = lib.get(disc["fp"], {})
        if watched:
            rec["watched"] = True
        else:
            rec.pop("watched", None)
        if rec:
            lib[disc["fp"]] = rec
        else:
            lib.pop(disc["fp"], None)
        save_library(lib)
    return jsonify({"watched": watched})


@app.post("/api/disc/<disc_id>/lookup")
def api_lookup(disc_id: str):
    """Search the web again for this disc's cover and details, optionally with another search term."""
    disc = get_disc(disc_id)
    payload = request.get_json(silent=True)
    query = str(payload.get("query", "")).strip()[:200] if isinstance(payload, dict) else ""
    is_cd = disc.get("kind") == "cd"
    if not is_cd and not TMDB_API_KEY:
        return jsonify({"error": "Set TMDB_API_KEY in the .env file to search for DVDs"}), 400
    if not _enrich_lock.acquire(timeout=60):
        return jsonify({"error": "A background lookup is still running, try again in a minute"}), 503
    try:
        found = (lookup_cd if is_cd else lookup_dvd)(disc, query or None)
    except (urllib.error.URLError, OSError, ValueError, KeyError) as exc:
        return jsonify({"error": f"Lookup failed: {exc}"}), 502
    finally:
        _enrich_lock.release()
    cover = True if not found else bool(disc.pop("_cover_found", True))
    return jsonify({"found": found, "cover": cover, "disc": disc_view(disc, load_library())})


@app.post("/api/disc/<disc_id>/cover")
def api_cover_upload(disc_id: str):
    disc = get_disc(disc_id)
    f = request.files.get("cover")
    if f is None:
        return jsonify({"error": "Missing cover file"}), 400
    data = f.read(MAX_COVER_BYTES + 1)
    if len(data) > MAX_COVER_BYTES:
        return jsonify({"error": "Image too large (max 8 MB)"}), 413
    ext = sniff_image(data)
    if not ext:
        return jsonify({"error": "Unsupported format (use JPG, PNG or WebP)"}), 400
    CUSTOM_COVER_DIR.mkdir(parents=True, exist_ok=True)
    for e in CUSTOM_EXTS:
        (CUSTOM_COVER_DIR / f"{disc['fp']}{e}").unlink(missing_ok=True)
    (CUSTOM_COVER_DIR / f"{disc['fp']}{ext}").write_bytes(data)
    return jsonify({"ok": True})


@app.delete("/api/disc/<disc_id>/cover")
def api_cover_delete(disc_id: str):
    disc = get_disc(disc_id)
    for e in CUSTOM_EXTS:
        (CUSTOM_COVER_DIR / f"{disc['fp']}{e}").unlink(missing_ok=True)
    return jsonify({"ok": True})


@app.post("/api/play/<disc_id>")
def api_play(disc_id: str):
    """Open a disc. {resume: true} continues where it was left; otherwise it starts over."""
    disc = get_disc(disc_id)
    payload = request.get_json(silent=True)
    want_resume = bool(payload.get("resume")) if isinstance(payload, dict) else False
    entry = get_resume().get(disc["fp"])
    resume = None
    if want_resume and entry:
        resume = {"title": int(entry.get("title") or 0), "time": max(0, int(entry.get("time", 0)) - 5)}
    elif entry:
        _set_resume(disc["fp"], None)  # started over on purpose
    _pending.clear()
    _pending.update(state=disc["state"], wait=True, resume=resume)
    write_state(disc["state"])
    title = load_library().get(disc["fp"], {}).get("title") or disc["title"]
    return jsonify({"player_url": "/player", "title": title, "kind": disc.get("kind", "dvd")})


@app.post("/api/stop")
def api_stop():
    write_state("")
    return jsonify({"ok": True})


@app.get("/player")
def player_page():
    return send_from_directory(app.static_folder, "player.html")


def playing_disc() -> dict | None:
    path = read_state()
    if not path:
        return None
    return next((d for d in get_catalog().values() if d["state"] == path), None)


def cd_tracks_view(disc: dict) -> list[dict]:
    return [
        {
            "n": t["n"],
            "title": t["title"],
            "artist": t["artist"],
            "start": t["start"],
            "length": None if t["length"] is None else round(t["length"], 2),
        }
        for t in disc["tracks"]
    ]


@app.get("/api/player")
def api_player():
    disc = playing_disc()
    out = {"url": neko_url(), "title": None, "id": None, "kind": "dvd"}
    if disc:
        out["title"] = load_library().get(disc["fp"], {}).get("title") or disc["title"]
        out["id"] = disc["id"]
        out["kind"] = disc.get("kind", "dvd")
        pend = _pending.get("resume") if _pending.get("state") == disc["state"] else None
        out["resume"] = bool(pend)
        out["resume_at"] = pend["time"] if pend else None
        if out["kind"] == "cd":
            out["artist"] = disc.get("artist", "")
            out["tracks"] = cd_tracks_view(disc)
            out["cover"] = f"/api/cover/{disc['id']}?v={cover_version(disc)}"
    return jsonify(out)


def cd_status(disc: dict) -> dict:
    """Playback state of an audio CD: time and length are relative to the current track."""
    st = VLC_RC.playback(with_length=not disc["raw"])
    spans = disc["tracks"]
    if not disc["raw"] and st["length"]:  # a single audio file: the last track ends with it
        spans = track_spans({"tracks": [{"n": t["n"], "title": t["title"], "artist": t["artist"],
                                          "start": t["start"]} for t in spans]}, float(st["length"]))
    pos = st["time"] or 0
    idx = locate(spans, pos)
    cur = spans[idx]
    start = int(cur["start"])
    length = None if cur["length"] is None else int(round(cur["length"]))
    return {
        "state": st["state"],
        "kind": "cd",
        "volume": st["volume"],
        "track": idx,
        "tracks": len(spans),
        "time": max(0, pos - start),
        "length": length,
        "abs": pos,
    }


@app.get("/api/player/status")
def api_player_status():
    if not read_state():
        return jsonify({"state": "idle"})
    try:
        disc = playing_disc()
        if disc and disc.get("kind") == "cd":
            st = cd_status(disc)
            if st["state"] in ("playing", "paused"):
                remember_position(disc, st["abs"], int(disc["duration"]) if disc.get("duration") else None, None)
            return jsonify(st)
        st = VLC_RC.status()
        if disc and st["state"] in ("playing", "paused"):
            remember_position(disc, st["time"], st["length"], st["title"])
        return jsonify(st)
    except VlcError:
        # VLC is still starting (or restarting for a new disc)
        return jsonify({"state": "offline"})


def _int_field(payload: dict, key: str, lo: int, hi: int) -> int:
    value = payload.get(key)
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"'{key}' must be a number")
    return max(lo, min(hi, int(value)))


def cd_command(disc: dict, action: str, payload: dict) -> bool:
    """Handle the commands that differ for audio CDs. Returns False if the action is not one of them."""
    spans = disc["tracks"]
    if action == "seek":  # seconds from the start of the current track
        pos = VLC_RC.get_time() or 0
        cur = spans[locate(spans, pos)]
        VLC_RC.seek(int(cur["start"]) + _int_field(payload, "value", 0, 86400))
    elif action in ("track_next", "chapter_next"):
        idx = locate(spans, VLC_RC.get_time() or 0)
        if idx + 1 >= len(spans):
            raise ValueError("This is the last track")
        VLC_RC.seek(int(spans[idx + 1]["start"]))
    elif action in ("track_prev", "chapter_prev"):
        pos = VLC_RC.get_time() or 0
        idx = locate(spans, pos)
        # like a CD player: restart the track, or go to the previous one if it just began
        if pos - int(spans[idx]["start"]) > 3 or idx == 0:
            VLC_RC.seek(int(spans[idx]["start"]))
        else:
            VLC_RC.seek(int(spans[idx - 1]["start"]))
    elif action == "track":  # play track number (index) N
        idx = _int_field(payload, "value", 0, len(spans) - 1)
        VLC_RC.seek(int(spans[idx]["start"]))
    else:
        return False
    return True


def apply_resume(disc: dict) -> None:
    """Jump to the saved position (once). The disc is still paused, so nothing is heard meanwhile."""
    res = _pending.get("resume") if _pending.get("state") == disc["state"] else None
    if not res:
        return
    _pending["resume"] = None
    if disc.get("kind") == "cd":
        VLC_RC.seek(int(res["time"]))
        return
    for _ in range(25):  # wait until VLC has opened the disc
        if (VLC_RC.get_length() or 0) > 0:
            break
        time.sleep(0.4)
    if res["title"] > 0:
        VLC_RC.set_title(res["title"])
        time.sleep(1.0)
    VLC_RC.seek(int(res["time"]))


def start_playback(disc: dict | None) -> None:
    """Let a disc that was opened paused start playing (once)."""
    waiting = bool(disc and _pending.get("state") == disc["state"] and _pending.get("wait"))
    st = VLC_RC.playback()
    if st["state"] == "paused" and (waiting or (st["time"] or 0) <= 2):
        VLC_RC.pause()
    if waiting:
        _pending["wait"] = False


@app.post("/api/player/cmd")
def api_player_cmd():
    """Control VLC: {action: toggle|seek|chapter_next|chapter_prev|volume|audio|subtitle|menu|track*, value?}."""
    if not read_state():
        return jsonify({"error": "Nothing is playing"}), 409
    payload = request.get_json(silent=True)
    if not isinstance(payload, dict):
        return jsonify({"error": "Invalid JSON"}), 400
    action = payload.get("action")
    disc = playing_disc()
    try:
        if action == "resume_apply":
            if disc:
                apply_resume(disc)
        elif action == "start":
            start_playback(disc)
        elif action == "nav":
            VLC_RC.nav(str(payload.get("value", "")))
        elif disc and disc.get("kind") == "cd" and cd_command(disc, action, payload):
            pass
        elif action == "toggle":
            VLC_RC.pause()
        elif action == "seek":
            VLC_RC.seek(_int_field(payload, "value", 0, 86400))
        elif action == "chapter_next":
            VLC_RC.next_chapter()
        elif action == "chapter_prev":
            VLC_RC.prev_chapter()
        elif action == "volume":
            VLC_RC.set_volume(_int_field(payload, "value", 0, 100))
        elif action == "audio":
            VLC_RC.set_audio(_int_field(payload, "value", 0, 10**6))
        elif action == "subtitle":
            VLC_RC.set_subtitle(_int_field(payload, "value", -1, 10**6))
        elif action == "menu":
            VLC_RC.disc_menu()
        else:
            return jsonify({"error": "Unknown action"}), 400
    except ValueError as exc:
        return jsonify({"error": str(exc)}), 400
    except VlcError:
        return jsonify({"error": "Player not ready"}), 503
    return jsonify({"ok": True})


@app.post("/api/rescan")
def api_rescan():
    return jsonify({"count": len(get_catalog(force=True))})


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=int(os.environ.get("PORT", "8080")))
