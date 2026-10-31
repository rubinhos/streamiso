"""Minimal client for VLC's remote-control (rc) interface.

VLC runs inside the Neko container with ``--extraintf=rc --rc-host=0.0.0.0:4212``.
The protocol is line based: after each command VLC replies and prints a ``> `` prompt.
Output formats below were checked against VLC 3.0.20.

Every call opens a short-lived connection, so a VLC restart (a new disc) never leaves
a stale socket behind.
"""
from __future__ import annotations

import re
import socket
import threading

PROMPT = b"> "
VOLUME_100 = 256  # rc volume scale: 256 is 100 % (maximum 512)


class VlcError(Exception):
    """VLC is not reachable or did not answer."""


class VlcRc:
    def __init__(self, host: str, port: int, timeout: float = 2.0):
        self.host = host
        self.port = port
        self.timeout = timeout
        self._lock = threading.Lock()

    # ----------------------------------------------------------------- transport
    def _read_until_prompt(self, sock: socket.socket) -> str:
        buf = b""
        while not buf.endswith(PROMPT):
            chunk = sock.recv(4096)
            if not chunk:
                raise VlcError("VLC closed the connection")
            buf += chunk
        return buf[: -len(PROMPT)].decode("utf-8", "replace")

    def run(self, commands: list[str]) -> list[str]:
        """Send commands in order on one connection and return one reply per command."""
        for cmd in commands:
            if "\n" in cmd or "\r" in cmd:
                raise ValueError("newline in command")
        with self._lock:
            try:
                with socket.create_connection((self.host, self.port), timeout=self.timeout) as sock:
                    sock.settimeout(self.timeout)
                    self._read_until_prompt(sock)  # banner
                    replies = []
                    for cmd in commands:
                        sock.sendall(cmd.encode("utf-8") + b"\n")
                        replies.append(self._read_until_prompt(sock))
                    return replies
            except (OSError, VlcError) as exc:
                raise VlcError(str(exc)) from exc

    # ------------------------------------------------------------------- parsing
    @staticmethod
    def _int(text: str) -> int | None:
        m = re.search(r"-?\d+", text)
        return int(m.group()) if m else None

    @staticmethod
    def _tracks(text: str) -> list[dict]:
        tracks = []
        for line in text.splitlines():
            m = re.match(r"\|\s*(-?\d+)\s*-\s*(.*?)(\s+\*)?\s*$", line)
            if m:
                tracks.append({"id": int(m.group(1)), "name": m.group(2), "selected": bool(m.group(3))})
        return tracks

    # ----------------------------------------------------------------------- API
    def status(self) -> dict:
        (st, now, length, atr, str_, chap, title) = self.run(
            ["status", "get_time", "get_length", "atrack", "strack", "chapter", "title"]
        )
        state = re.search(r"\(\s*state\s+(\w+)\s*\)", st)
        vol = re.search(r"audio volume:\s*(\d+)", st)
        raw_volume = int(vol.group(1)) if vol else None
        audio = self._tracks(atr)
        subs = self._tracks(str_)
        return {
            "state": state.group(1) if state else "unknown",
            "time": self._int(now),
            "length": self._int(length),
            "volume": None if raw_volume is None else min(100, round(raw_volume * 100 / VOLUME_100)),
            "chapter": self._int(chap),
            "title": self._int(title),
            # -1 is VLC's "Disable" entry; keep it for subtitles, hide it for audio
            "audio": [t for t in audio if t["id"] >= 0],
            "subtitles": subs,
        }

    def playback(self, with_length: bool = False) -> dict:
        """State, volume and position only (used for audio CDs, which have no tracks to list)."""
        cmds = ["status", "get_time"] + (["get_length"] if with_length else [])
        replies = self.run(cmds)
        state = re.search(r"\(\s*state\s+(\w+)\s*\)", replies[0])
        vol = re.search(r"audio volume:\s*(\d+)", replies[0])
        raw_volume = int(vol.group(1)) if vol else None
        return {
            "state": state.group(1) if state else "unknown",
            "time": self._int(replies[1]),
            "length": self._int(replies[2]) if with_length else None,
            "volume": None if raw_volume is None else min(100, round(raw_volume * 100 / VOLUME_100)),
        }

    def get_time(self) -> int | None:
        return self._int(self.run(["get_time"])[0])

    def get_length(self) -> int | None:
        return self._int(self.run(["get_length"])[0])

    def set_title(self, number: int) -> None:
        self.run([f"title {int(number)}"])

    NAV = {"up": "up", "down": "down", "left": "left", "right": "right", "activate": "activate"}

    def nav(self, direction: str) -> None:
        """Move in a DVD menu ("up", "down", "left", "right") or press the highlighted button ("activate")."""
        if direction not in self.NAV:
            raise ValueError("unknown direction")
        self.run([f"key key-nav-{self.NAV[direction]}"])

    def pause(self) -> None:
        self.run(["pause"])

    def seek(self, seconds: int) -> None:
        self.run([f"seek {max(0, int(seconds))}"])

    def set_volume(self, percent: int) -> None:
        percent = max(0, min(100, int(percent)))
        self.run([f"volume {round(percent * VOLUME_100 / 100)}"])

    def next_chapter(self) -> None:
        self.run(["chapter_n"])

    def prev_chapter(self) -> None:
        self.run(["chapter_p"])

    def set_audio(self, track_id: int) -> None:
        self.run([f"atrack {int(track_id)}"])

    def set_subtitle(self, track_id: int) -> None:
        self.run([f"strack {int(track_id)}"])

    def disc_menu(self) -> None:
        self.run(["key key-disc-menu"])
