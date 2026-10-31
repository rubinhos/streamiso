"""Audio CD images: reads .cue sheets (BIN/CUE).

Only the common "one image file" layout is supported: a single FILE entry that holds every
track, all of them AUDIO. Raw ``BINARY`` images are 44.1 kHz, 16-bit, stereo, 2352-byte
sectors, 75 sectors per second. VLC plays them directly as one continuous stream, and a
track is just a position in it (which also gives gapless playback, like a real CD).
"""
from __future__ import annotations

import re
from pathlib import Path

SECTORS_PER_SECOND = 75
BYTES_PER_SECOND = 44100 * 2 * 2  # 44.1 kHz * 16 bit * stereo = 176400

AUDIO_FILE_TYPES = {"WAVE", "MP3", "AIFF", "FLAC"}  # decoded by VLC as ordinary files


class CueError(Exception):
    """The cue sheet cannot be used; the message says why."""


def _read_text(path: Path) -> str:
    raw = path.read_bytes()[:512 * 1024]
    for enc in ("utf-8-sig", "cp1252", "latin-1"):
        try:
            return raw.decode(enc)
        except UnicodeDecodeError:
            continue
    return raw.decode("latin-1", "replace")


def _quoted(rest: str) -> str:
    rest = rest.strip()
    if rest.startswith('"'):
        end = rest.find('"', 1)
        return rest[1:end] if end > 0 else rest[1:]
    return rest.split()[0] if rest else ""


def _msf(text: str) -> float:
    m = re.fullmatch(r"(\d+):(\d{1,2}):(\d{1,2})", text.strip())
    if not m:
        raise CueError(f"bad INDEX time {text!r}")
    mm, ss, ff = (int(x) for x in m.groups())
    return mm * 60 + ss + ff / SECTORS_PER_SECOND


def _find_file(cue: Path, name: str) -> Path | None:
    name = name.replace("\\", "/").split("/")[-1]  # sheets written on Windows may carry a path
    direct = cue.parent / name
    if direct.is_file():
        return direct
    lowered = name.lower()
    try:
        for p in cue.parent.iterdir():  # tolerate a different letter case
            if p.name.lower() == lowered and p.is_file():
                return p
    except OSError:
        pass
    return None


def parse_cue(cue: Path) -> dict:
    """Parse a cue sheet. Raises CueError when the image is not usable.

    Returns {text, album, artist, year, genre, bin, byte_order, raw, tracks, duration}.
    ``tracks`` is a list of {n, title, artist, start}, start in seconds from the beginning of the
    image. ``duration`` is the image length in seconds for raw images, else None.
    """
    text = _read_text(cue)
    album = artist = genre = ""
    year = None
    files: list[tuple[str, str]] = []
    tracks: list[dict] = []
    cur: dict | None = None

    for line in text.splitlines():
        line = line.strip()
        if not line:
            continue
        key, _, rest = line.partition(" ")
        key = key.upper()
        if key == "FILE":
            m = re.match(r'(".*?"|\S+)\s*(\S*)', rest.strip())
            if not m:
                raise CueError("bad FILE line")
            files.append((_quoted(m.group(1)), m.group(2).upper()))
        elif key == "TRACK":
            parts = rest.split()
            if len(parts) < 2:
                raise CueError("bad TRACK line")
            if parts[1].upper() != "AUDIO":
                raise CueError(f"track {parts[0]} is {parts[1]}, only audio tracks are supported")
            cur = {"n": len(tracks) + 1, "title": "", "artist": "", "start": None}
            tracks.append(cur)
        elif key == "TITLE":
            if cur is None:
                album = _quoted(rest)
            else:
                cur["title"] = _quoted(rest)
        elif key == "PERFORMER":
            if cur is None:
                artist = _quoted(rest)
            else:
                cur["artist"] = _quoted(rest)
        elif key == "INDEX" and cur is not None:
            parts = rest.split()
            if len(parts) >= 2 and parts[0] == "01":
                cur["start"] = _msf(parts[1])
        elif key == "REM":
            sub, _, val = rest.strip().partition(" ")
            if sub.upper() == "GENRE":
                genre = _quoted(val)
            elif sub.upper() == "DATE":
                m = re.search(r"(?:19|20)\d{2}", val)
                year = m.group() if m else None

    if not files:
        raise CueError("no FILE entry")
    if len(files) > 1:
        raise CueError("images split into one file per track are not supported yet")
    if not tracks:
        raise CueError("no tracks")
    if any(t["start"] is None for t in tracks):
        raise CueError("a track has no INDEX 01")
    starts = [t["start"] for t in tracks]
    if starts != sorted(starts):
        raise CueError("tracks are not in order")

    name, ftype = files[0]
    target = _find_file(cue, name)
    if target is None:
        raise CueError(f"image file {name!r} not found next to the cue sheet")

    raw = ftype in ("BINARY", "MOTOROLA")
    if not raw and ftype not in AUDIO_FILE_TYPES:
        raise CueError(f"unsupported file type {ftype}")
    duration = None
    if raw:
        size = target.stat().st_size
        duration = size / BYTES_PER_SECOND
        if starts[-1] >= duration:
            raise CueError("the last track starts after the end of the image file")

    return {
        "text": text,
        "album": album,
        "artist": artist,
        "year": year,
        "genre": genre,
        "bin": target,
        "byte_order": "s16b" if ftype == "MOTOROLA" else "s16l",
        "raw": raw,
        "tracks": tracks,
        "duration": duration,
    }


def track_spans(info: dict, total: float | None = None) -> list[dict]:
    """Tracks with start/end/length in seconds. ``total`` overrides the duration (non-raw images)."""
    end_of_disc = total if total is not None else info["duration"]
    out = []
    tracks = info["tracks"]
    for i, t in enumerate(tracks):
        end = tracks[i + 1]["start"] if i + 1 < len(tracks) else end_of_disc
        out.append({
            "n": t["n"],
            "title": t["title"] or f"Track {t['n']}",
            "artist": t["artist"],
            "start": t["start"],
            "end": end,
            "length": None if end is None else max(0.0, end - t["start"]),
        })
    return out


def start_second(track: dict) -> int:
    """Whole second to seek to for a track (VLC's rc interface seeks in whole seconds)."""
    return int(track["start"])


def locate(spans: list[dict], position: int) -> int:
    """Index of the track that contains ``position`` (whole seconds from the start of the image)."""
    idx = 0
    for i, t in enumerate(spans):
        if position >= start_second(t):
            idx = i
    return idx
