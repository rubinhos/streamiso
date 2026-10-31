"""Album lookup on MusicBrainz and cover art from the Cover Art Archive (both free, no key).

MusicBrainz asks clients to identify themselves and to stay under one request per second.
https://musicbrainz.org/doc/MusicBrainz_API  ·  https://musicbrainz.org/doc/Cover_Art_Archive/API
"""
from __future__ import annotations

import json
import os
import re
import time
import urllib.error
import urllib.parse
import urllib.request

MB_URL = os.environ.get("MUSICBRAINZ_URL", "https://musicbrainz.org/ws/2").rstrip("/")
CAA_URL = os.environ.get("COVERART_URL", "https://coverartarchive.org").rstrip("/")
MIN_SCORE = 85
MAX_COVER_BYTES = 8 * 1024 * 1024


def user_agent(version: str) -> str:
    contact = os.environ.get("MUSICBRAINZ_CONTACT", "").strip()
    return f"Streamiso/{version}" + (f" ( {contact} )" if contact else "")


def _norm(text: str) -> str:
    return re.sub(r"[^a-z0-9]+", " ", text.lower()).strip()


def _lucene(text: str) -> str:
    """Quote a value for a Lucene phrase query."""
    return '"' + text.replace("\\", " ").replace('"', " ").strip() + '"'


def _get(url: str, version: str, timeout: int = 15, limit: int | None = None) -> bytes:
    req = urllib.request.Request(url, headers={"User-Agent": user_agent(version), "Accept": "*/*"})
    with urllib.request.urlopen(req, timeout=timeout) as resp:  # follows the Cover Art Archive redirect
        return resp.read(limit + 1) if limit else resp.read()


def _variants(title: str) -> list[tuple[str, bool]]:
    """Titles to try, most specific first: [(title, strict)]. Shorter ones are matched by words."""
    out: list[tuple[str, bool]] = []

    def add(q: str, strict: bool) -> None:
        q = re.sub(r"\s+", " ", q).strip()
        if q and _norm(q) not in (_norm(x) for x, _ in out):
            out.append((q, strict))

    add(title, False)
    for sep in (":", " - ", " \u2013 ", " \u2014 "):
        if sep in title:
            head, _, tail = title.partition(sep)
            add(head, True)
            add(tail, True)
    words = _norm(title).split()
    for n in range(len(words) - 1, 2, -1):  # keep at least three words
        add(" ".join(words[:n]), True)
    return out[:6]


def _search(title: str, artist: str, strict: bool, version: str) -> dict | None:
    query = f"releasegroup:{_lucene(title)}"
    if artist:
        query += f" AND artist:{_lucene(artist)}"
    url = f"{MB_URL}/release-group/?" + urllib.parse.urlencode({"query": query, "fmt": "json", "limit": 8})
    data = json.loads(_get(url, version))
    want_title, want_artist = _norm(title), _norm(artist)
    want_words = set(want_title.split())
    for rg in data.get("release-groups", []):
        if int(rg.get("score", 0)) < MIN_SCORE:
            continue
        got = _norm(rg.get("title", ""))
        if strict:
            # a shortened query must be fully contained in the album title (a near match is more
            # often another album than the right one)
            if not want_words <= set(got.split()):
                continue
        elif got != want_title and not (want_words <= set(got.split()) or set(got.split()) <= want_words):
            continue
        credit = " ".join(c.get("name", "") for c in rg.get("artist-credit", []) if isinstance(c, dict))
        if want_artist and want_artist not in _norm(credit) and _norm(credit) not in want_artist:
            continue
        tags = sorted(rg.get("tags", []), key=lambda t: -int(t.get("count", 0)))
        year = (rg.get("first-release-date") or "")[:4]
        return {
            "mbid": rg["id"],
            "title": rg.get("title", ""),
            "artist": credit,
            "year": year if year.isdigit() else "",
            "genres": [t["name"].title() for t in tags[:3] if t.get("name")],
        }
    return None


def lookup_album(title: str, artist: str, version: str) -> dict | None:
    """Best matching release group: {mbid, title, artist, year, genres} or None.

    Tries the full title with the artist, then the full title alone (cue sheets often carry a wrong or
    generic artist, e.g. "Various Artists"), then shorter forms of the title.
    """
    if not title:
        return None
    first = True
    for q, strict in _variants(title):
        for a in ([artist, ""] if artist and not strict else [""]):
            if not first:
                time.sleep(1.1)  # MusicBrainz: at most one request per second
            first = False
            hit = _search(q, a, strict, version)
            if hit:
                return hit
    return None


def _image(path: str, version: str) -> bytes | None:
    try:
        data = _get(f"{CAA_URL}/{path}", version, timeout=25, limit=MAX_COVER_BYTES)
    except urllib.error.HTTPError as exc:
        if exc.code in (404, 400):
            return None
        raise
    if len(data) > MAX_COVER_BYTES:
        return None
    if data[:3] == b"\xff\xd8\xff" or data[:8] == b"\x89PNG\r\n\x1a\n":
        return data
    return None


def fetch_cover(mbid: str, version: str) -> bytes | None:
    """Front cover (500 px) of a release group, or None if the archive has none.

    The archive often has art only for individual releases (a specific pressing) and not for the
    release group, so when the group has none, the releases inside it are tried one by one.
    """
    data = _image(f"release-group/{mbid}/front-500", version)
    if data:
        return data
    time.sleep(1.1)
    url = f"{MB_URL}/release?" + urllib.parse.urlencode({"release-group": mbid, "fmt": "json", "limit": 25})
    try:
        releases = json.loads(_get(url, version)).get("releases", [])
    except (urllib.error.URLError, OSError, ValueError):
        return None
    tried = 0
    for rel in releases:
        art = rel.get("cover-art-archive") or {}
        if art and not (art.get("front") or art.get("artwork")):
            continue  # the archive says this release has no front cover
        if tried >= 6:
            break
        tried += 1
        data = _image(f"release/{rel['id']}/front-500", version)
        if data:
            return data
    return None
