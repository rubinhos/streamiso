"""Plain video (HLS) for TVs whose WebRTC cannot connect to Neko.

Some TVs have WebRTC too old for Neko: webOS 3 (Chrome 38) only speaks DTLS 1.0, and Neko needs DTLS 1.2.
They can still play an ordinary video stream. docker-compose.yml sets Neko's broadcast to write the same
screen and sound it streams over WebRTC as HLS segments, in a folder it shares with the catalog. This
module starts that broadcast through Neko's API when such a TV asks, serves its files, and stops it once
nobody has fetched them for a while, so the extra encoding only runs while someone watches this way.
"""
from __future__ import annotations

import json
import re
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path

# Only what Neko's hlssink writes (see NEKO_CAPTURE_BROADCAST_PIPELINE in docker-compose.yml).
FILE_NAME = re.compile(r"^(index\.m3u8|seg\d{5}\.ts)$")


class HlsError(Exception):
    pass


class HlsBroadcast:
    def __init__(self, folder: Path, neko_url: str, user: str, password: str, idle_seconds: float = 30):
        self.folder = folder
        self.neko_url = neko_url.rstrip("/")
        self.user = user
        self.password = password
        self.idle_seconds = idle_seconds
        self._lock = threading.Lock()
        self._token: str | None = None
        self._active = False
        self._last_fetch = 0.0
        self._watcher: threading.Thread | None = None

    def available(self) -> bool:
        """The folder is mounted and Neko has an admin password to start the broadcast with."""
        return self.folder.is_dir() and bool(self.password)

    # ------------------------------------------------------------------ Neko's API
    def _request(self, method: str, path: str, body: dict | None = None, token: str | None = None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.neko_url + path, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=5) as resp:
                raw = resp.read()
                return resp.status, (json.loads(raw) if raw else None)
        except urllib.error.HTTPError as exc:
            return exc.code, None
        except (urllib.error.URLError, OSError, ValueError) as exc:
            raise HlsError(f"Neko did not answer: {exc}") from exc

    def _call(self, method: str, path: str, body: dict | None = None):
        """An admin call. The session is kept, and opened again if Neko has forgotten it (a restart)."""
        for _ in range(2):
            if not self._token:
                status, data = self._request("POST", "/api/login", {"username": self.user, "password": self.password})
                if status != 200 or not isinstance(data, dict) or not data.get("token"):
                    raise HlsError(f"Neko refused the admin login ({status})")
                self._token = data["token"]
            status, data = self._request(method, path, body, self._token)
            if status != 401:
                return status, data
            self._token = None
        raise HlsError("Neko refused the admin session")

    # ------------------------------------------------------------------ start and stop
    def start(self) -> None:
        """Starts the broadcast, or keeps the one that is running. Raises HlsError."""
        with self._lock:
            status, data = self._call("GET", "/api/room/broadcast")
            if status == 200 and isinstance(data, dict) and data.get("is_active"):
                self._active = True
            else:
                # Old segments and an old playlist would be played first.
                self.folder.chmod(0o777)  # Neko writes as its own user
                for f in self.folder.iterdir():
                    if FILE_NAME.match(f.name):
                        f.unlink(missing_ok=True)
                # Neko needs a URL to start; the pipeline writes to the shared folder and ignores it.
                status, _ = self._call("POST", "/api/room/broadcast/start", {"url": "hls"})
                if status not in (200, 204, 422):  # 422: it was already broadcasting
                    raise HlsError(f"Neko did not start the broadcast ({status})")
                self._active = True
            self._last_fetch = time.monotonic()
            if not self._watcher or not self._watcher.is_alive():
                self._watcher = threading.Thread(target=self._watch, daemon=True)
                self._watcher.start()

    def wait_ready(self, timeout: float = 10) -> bool:
        """True once the playlist lists two segments, enough for a TV to start playing."""
        end = time.monotonic() + timeout
        playlist = self.folder / "index.m3u8"
        while time.monotonic() < end:
            try:
                if playlist.read_text("utf-8").count(".ts") >= 2:
                    return True
            except OSError:
                pass
            time.sleep(0.2)
        return False

    def stop(self) -> None:
        with self._lock:
            if not self._active:
                return
            self._active = False
            try:
                self._call("POST", "/api/room/broadcast/stop")  # 422 if it had stopped already
            except HlsError:
                pass

    def _watch(self) -> None:
        # A TV fetches the playlist every second or two while it plays; silence means it has gone.
        while self._active:
            time.sleep(5)
            if self._active and time.monotonic() - self._last_fetch > self.idle_seconds:
                self.stop()

    # ------------------------------------------------------------------ files
    def file(self, name: str) -> Path | None:
        """The playlist or a segment, if the name is one Neko writes and it exists. Counts as a fetch."""
        if not FILE_NAME.match(name):
            return None
        path = self.folder / name
        if not path.is_file():
            return None
        self._last_fetch = time.monotonic()
        return path
