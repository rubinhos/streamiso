# Differential test: runs the Python catalog (gunicorn, ../catalog) and the Go one side by side, each
# with its own fake TMDB, MusicBrainz and VLC, on the same media fixtures, sends both the same
# requests and compares the answers, the files they write, what they send to VLC and what they ask
# TMDB and MusicBrainz. Run it in the Python catalog image, from the repo (see go/README.md):
#   docker run --rm -v "$PWD:/w" streamiso-catalog python -I -u /w/go/tools/difftest.py /w/go/tools/catalog-go
import collections, re, hashlib, json, os, shutil, socket, subprocess, sys, threading, time, urllib.parse, urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

GO_BIN = sys.argv[1] if len(sys.argv) > 1 else "/w/go/tools/catalog-go"
ROOT = "/tmp/fx"
shutil.rmtree(ROOT, ignore_errors=True)
DVD, CD = f"{ROOT}/dvd", f"{ROOT}/cd"


def pvd(label: str) -> bytes:
    d = bytearray(2048)
    d[0] = 1
    d[1:6] = b"CD001"
    d[40:72] = label.encode().ljust(32, b" ")
    d[813:830] = b"2005010112000000\x00"
    return bytes(d)


def iso(rel: str, label: str | None = None, size: int = 0x9000) -> None:
    p = f"{DVD}/{rel}"
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, "wb") as f:
        f.write(b"\0" * 0x8000)
        if label is not None:
            f.write(pvd(label))
        f.truncate(size)


iso("Titanic Disc 1.iso", "TITANIC_D1")
iso("Titanic Disc 2.iso", "TITANIC_D2")
iso("Friends S01 D1.iso", "FRIENDS_S1_D1")
iso("Friends S01 D2.iso", "FRIENDS_S1_D2")
iso("Friends S02 D1.iso", "FRIENDS_S2_D1")
iso("Movie Name (1999)/VIDEO_TS.iso", "MOVIE")
open(f"{DVD}/Movie Name (1999)/cover.jpg", "wb").write(b"\xff\xd8\xff folder cover")
iso("Generic/DVD.iso", "COOL_MOVIE_2004")
iso("Generic/disc.iso", "")
iso("Unknown Film.iso", None)
iso("Brazil Film.iso", "BRAZIL")
iso("Now And Then.iso", "NOW_AND_THEN")
open(f"{DVD}/Now And Then.jpg", "wb").write(b"\xff\xd8\xff user cover")
iso("nested/deeper/Will & Grace (2017) S01 D2.iso", "WG")
iso("lower.ISO", "LOWER")
iso("AMELIE_POULAIN.iso", "AMELIE")
iso("Pocket Show Special Episode Title.iso", "POCKET")
open(f"{DVD}/notes.txt", "w").write("not a disc")

os.makedirs(CD)
open(f"{CD}/Album One.cue", "w").write('REM GENRE Rock\nREM DATE 1999\nPERFORMER "The Band"\nTITLE "Album One"\n'
                                       'FILE "Album One.bin" BINARY\n  TRACK 01 AUDIO\n    TITLE "Intro"\n    INDEX 01 00:00:00\n'
                                       '  TRACK 02 AUDIO\n    TITLE "Song"\n    PERFORMER "Guest"\n    INDEX 00 00:09:70\n    INDEX 01 00:10:00\n'
                                       '  TRACK 03 AUDIO\n    INDEX 01 00:15:37\n')
with open(f"{CD}/Album One.bin", "wb") as f:
    f.truncate(176400 * 20)
open(f"{CD}/Wave Album.cue", "w").write('TITLE "Wave Album"\nPERFORMER "Various Artists"\nFILE "track.wav" WAVE\n'
                                        '  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n  TRACK 02 AUDIO\n    INDEX 01 01:00:00\n')
open(f"{CD}/track.wav", "wb").write(b"RIFF....WAVE")
open(f"{CD}/Broken.cue", "w").write('FILE "missing.bin" BINARY\n  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n')
os.makedirs(f"{CD}/latin")
open(f"{CD}/latin/Latin.cue", "wb").write('TITLE "Canção Nº1"\nFILE "LATIN.BIN" BINARY\n  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n'.encode("cp1252"))
with open(f"{CD}/latin/latin.bin", "wb") as f:
    f.truncate(176400 * 5)
open(f"{CD}/latin/cover.png", "wb").write(b"\x89PNG\r\n\x1a\n folder png")
open(f"{CD}/Data.cue", "w").write('FILE "Data.bin" BINARY\n  TRACK 01 MODE1/2352\n    INDEX 01 00:00:00\n')
open(f"{CD}/Data.bin", "wb").write(b"x")
open(f"{CD}/Unknown Album.cue", "w").write('TITLE "Unknown Album"\nFILE "u.bin" BINARY\n  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n')
with open(f"{CD}/u.bin", "wb") as f:
    f.truncate(176400 * 3)


# ------------------------------------------------------------------------------- fakes
class Fake:
    """Fake TMDB + MusicBrainz + Cover Art Archive + image host, one per catalog."""

    def __init__(self):
        self.calls = []
        fake = self

        class H(BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_GET(self):
                u = urllib.parse.urlparse(self.path)
                q = dict(urllib.parse.parse_qsl(u.query))
                q.pop("api_key", None)
                fake.calls.append((u.path, sorted(q.items())))
                code, ct, body = fake.answer(u.path, q)
                self.send_response(code)
                self.send_header("Content-Type", ct)
                self.end_headers()
                self.wfile.write(body)

        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), H)
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()
        self.url = f"http://127.0.0.1:{self.srv.server_port}"

    def answer(self, p, q):
        J = lambda d, code=200: (code, "application/json", json.dumps(d).encode())
        lang = q.get("language")
        if p.startswith("/img/"):
            return 200, "image/jpeg", b"\xff\xd8\xff" + p.encode()
        if p.startswith("/caa/release-group/rg1/"):
            return 200, "image/jpeg", b"\xff\xd8\xff album one"
        if p.startswith("/caa/"):
            return 404, "text/plain", b"no"
        if p == "/mb/release-group/":
            query = q["query"]
            if "album one" in query.lower():
                return J({"release-groups": [
                    {"id": "rg0", "score": 70, "title": "Album One"},
                    {"id": "rg1", "score": 100, "title": "Album One", "first-release-date": "1999-05-01",
                     "artist-credit": [{"name": "The Band"}],
                     "tags": [{"name": "rock", "count": 3}, {"name": "pop rock", "count": 5}, {"name": "x", "count": 1},
                              {"name": "indie", "count": 4}]}]})
            if "wave album" in query.lower():
                return J({"release-groups": [{"id": "rg2", "score": 90, "title": "Wave Album", "first-release-date": "",
                                              "artist-credit": [{"name": "Various Artists"}]}]})
            return J({"release-groups": []})
        if p == "/mb/release":
            return J({"releases": [{"id": "r1", "cover-art-archive": {"front": False, "artwork": False}},
                                   {"id": "r2"}]})
        if p.startswith("/genre/"):
            return J({"genres": [{"id": 18, "name": "Drama"}, {"id": 35, "name": "Comédia"}, {"id": 10751, "name": "Família"}]})
        if p == "/search/movie":
            t = q["query"].lower()
            r = []
            if "titanic" in t:
                r = [{"id": 597, "title": "Titanic", "release_date": "1997-12-19", "poster_path": "/t97.jpg",
                      "genre_ids": [18], "overview": "Um navio", "vote_average": 7.9},
                     {"id": 16535, "title": "Titanic", "release_date": "1953-04-16", "poster_path": "/t53.jpg", "genre_ids": []},
                     {"id": 1, "title": "No poster"}]
            elif "brazil" in t:
                r = [{"id": 68, "title": "Brazil: O Filme", "release_date": "1985-02-20", "poster_path": "/br.jpg",
                      "genre_ids": [35, 99], "overview": "", "vote_average": 7.8}]
            elif "now and then" in t:
                r = [{"id": 12, "title": "Amigas para Sempre", "original_title": "Now and Then", "release_date": "1995",
                      "poster_path": "/nat.jpg", "genre_ids": [35, 18], "overview": "Quatro amigas"}]
            elif "cool movie" in t:
                r = [{"id": 77, "title": "Cool Movie", "release_date": "2004-01-01", "poster_path": "/cool.jpg"}]
            elif t == "movie name" or "amelie" in t:
                r = [{"id": 194, "title": "O Fabuloso Destino de Amélie Poulain", "original_title": "Amélie",
                      "release_date": "2001", "poster_path": "/am.jpg", "genre_ids": [35]}]
            elif t == "pocket show":
                r = [{"id": 5, "title": "Pocket Show", "poster_path": "/ps.jpg"}]
            if q.get("year") and r:
                r = [x for x in r if (x.get("release_date") or "").startswith(q["year"])] or r[:1]
            return J({"results": r, "page": 1})
        if p == "/search/tv":
            t = q["query"].lower()
            r = []
            if "friends" in t:
                r = [{"id": 1668, "name": "Friends", "first_air_date": "1994-09-22", "poster_path": "/fr.jpg",
                      "genre_ids": [35], "overview": "Seis amigos", "vote_average": 8.4}]
            elif "will" in t and "grace" in t:
                r = [{"id": 1, "name": "Will & Grace", "first_air_date": "1998-09-21", "poster_path": "/wg98.jpg"},
                     {"id": 74321, "name": "Will & Grace", "first_air_date": "2017-09-28", "poster_path": "/wg17.jpg"}]
            return J({"results": r})
        if p == "/tv/1668/season/1":
            return J({"poster_path": "/fr1.jpg"})
        if p == "/tv/74321/season/1":
            return J({"poster_path": None})
        if p.startswith("/tv/") and "/season/" in p and not p.endswith("/images"):
            return J({"status_message": "not found"}, 404)
        if p == "/movie/68":
            return J({"title": "Brazil: O Filme", "release_date": "1985-02-20",
                      "overview": "English synopsis" if lang == "en-US" else "", "genres": [{"id": 35, "name": "Comédia"}]})
        if p == "/movie/16535":
            return J({"title": "Titanic", "release_date": "1953-04-16", "overview": "Old one",
                      "genres": [{"id": 18, "name": "Drama"}, {"name": ""}], "vote_average": 6.0})
        if p == "/movie/597":
            return J({"title": "Titanic", "release_date": "1997-12-19", "overview": "Um navio", "genres": [], "vote_average": 7.9})
        if p == "/movie/597/images":
            return J({"posters": [{"file_path": "/t97de.jpg", "iso_639_1": "de"}, {"file_path": "/t97.jpg", "iso_639_1": None},
                                  {"file_path": "/t97en.jpg", "iso_639_1": "en"}, {"file_path": "/t97b.jpg", "iso_639_1": "pt"},
                                  {"file_path": "/t97.jpg"}, {"iso_639_1": "pt"}]})
        if p == "/tv/1668/season/1/images":
            return J({"posters": [{"file_path": "/fr1.jpg"}, {"file_path": "/fr.jpg"}]})
        if p == "/tv/1668/season/2/images":
            return J({}, 404)
        if p.endswith("/images"):
            return J({"posters": [{"file_path": "/fr.jpg", "iso_639_1": "pt"}, {"file_path": "/x.jpg", "iso_639_1": "fr"}]})
        if p == "/tv/1668":
            return J({"name": "Friends", "first_air_date": "1994-09-22", "overview": "Seis amigos (detalhes)",
                      "genres": [{"id": 35, "name": "Comédia"}], "vote_average": 8.4})
        if re.fullmatch(r"/(movie|tv)/\d+", p):
            return J({"title": "Generic", "overview": "Generic synopsis" if lang == "en-US" else "", "genres": []})
        return J({"status_message": "nope"}, 404)


class FakeVLC:
    """Answers VLC's rc commands and logs them."""
    TRACKS = ("+----[ Audio Track ]\r\n| -1 - Disable\r\n| 1 - English *\r\n| 2 - Português - [Português]\r\n"
              "+----[ end of Audio Track ]")
    SUBS = "+----[ Subtitle Track ]\n| -1 - Disable *\n| 3 - Legenda\n+----[ end of Subtitle Track ]"

    def __init__(self):
        self.log = []
        self.state, self.time, self.length = "playing", 1234, 6000
        self.sock = socket.socket()
        self.sock.bind(("127.0.0.1", 0))
        self.sock.listen(20)
        self.port = self.sock.getsockname()[1]
        threading.Thread(target=self.serve, daemon=True).start()

    def serve(self):
        while True:
            c, _ = self.sock.accept()
            threading.Thread(target=self.client, args=(c,), daemon=True).start()

    def reply(self, cmd):
        if cmd == "status":
            return f"( new input: file:///x )\n( audio volume: 205 )\n( state {self.state} )"
        if cmd == "get_time":
            return str(self.time)
        if cmd == "get_length":
            return str(self.length)
        if cmd == "atrack":
            return self.TRACKS
        if cmd == "strack":
            return self.SUBS
        if cmd == "chapter":
            return "4"
        if cmd == "title":
            return "1"
        return ""

    def client(self, c):
        f = c.makefile("rb")
        c.sendall(b"VLC media player 3.0.20 Vetinari\nCommand Line Interface initialized. Type `help' for help.\n> ")
        for line in f:
            cmd = line.decode().strip()
            self.log.append(cmd)
            r = self.reply(cmd)
            c.sendall((r + "\n> " if r else "> ").encode())
        c.close()


# ------------------------------------------------------------------------------- the two servers
def compute_fps():
    """The fingerprints of the fixtures, from the Python code (for the seeded library.json)."""
    os.environ.update(DVD_DIR=DVD, CD_DIR=CD, DATA_DIR="/tmp/fx-scan")
    sys.path.insert(0, "/w/catalog")
    import app as A
    return {d["rel"]: d["fp"] for d in A.scan().values()}


FPS = compute_fps()
servers = {}
for name, port in (("py", 9001), ("go", 9002)):
    data = f"/tmp/fx-data-{name}"
    shutil.rmtree(data, ignore_errors=True)
    os.makedirs(data)
    lib = {FPS["Generic/DVD.iso"]: {"title": "Generic Edited", "series": "x"}, FPS["lower.ISO"]: {"series": "only"},
           FPS["AMELIE_POULAIN.iso"]: {"sort_title": "Aaa", "genres": ["Romance"], "year": 2001, "cover_fill": False}}
    json.dump(lib, open(f"{data}/library.json", "w"), indent=1)
    json.dump({FPS["Titanic Disc 1.iso"]: {"time": 600, "length": 6000, "title": 1, "updated": 1},
               FPS["Album One.cue"]: {"time": 12, "length": None, "title": 0}}, open(f"{data}/resume.json", "w"))
    fake, vlc = Fake(), FakeVLC()
    env = dict(os.environ, DVD_DIR=DVD, CD_DIR=CD, DATA_DIR=data, STATE_FILE=f"/tmp/fx-state-{name}/current",
               TMDB_API_KEY="k", TMDB_LANGUAGE="pt-BR", TMDB_API_URL=fake.url, TMDB_IMAGE_URL=fake.url + "/img/w500",
               MUSICBRAINZ_URL=fake.url + "/mb", COVERART_URL=fake.url + "/caa", VLC_RC_HOST="127.0.0.1",
               VLC_RC_PORT=str(vlc.port), CORS_ORIGIN="null, http://tv.local", NEKO_PORT="8081", NEKO_PASSWORD="p w&x",
               MUSICBRAINZ_CONTACT="test@example.com", PORT=str(port), STATIC_DIR="/w/catalog/static")
    if name == "py":
        cmd = ["gunicorn", "-b", f"127.0.0.1:{port}", "-w", "1", "--threads", "8", "--log-level", "warning", "app:app"]
        proc = subprocess.Popen(cmd, cwd="/w/catalog", env=env)
    else:
        proc = subprocess.Popen([GO_BIN], env=env, cwd="/tmp")
    servers[name] = {"port": port, "fake": fake, "vlc": vlc, "proc": proc, "data": data, "state": env["STATE_FILE"]}

for s in servers.values():
    for _ in range(100):
        try:
            urllib.request.urlopen(f"http://127.0.0.1:{s['port']}/healthz", timeout=1)
            break
        except OSError:
            time.sleep(0.2)


def req(name, method, path, body=None, headers=None, raw=None):
    h = dict(headers or {})
    data = raw
    if body is not None:
        data = json.dumps(body).encode()
        h.setdefault("Content-Type", "application/json")
    r = urllib.request.Request(f"http://127.0.0.1:{servers[name]['port']}{path}", data=data, method=method, headers=h)
    try:
        resp = urllib.request.urlopen(r, timeout=90)
        code, rh, content = resp.status, resp.headers, resp.read()
    except urllib.error.HTTPError as e:
        code, rh, content = e.code, e.headers, e.read()
    return code, rh, content


def norm(v):
    """Volatile values (mtimes, the fakes' ports) only by whether they are set."""
    if isinstance(v, str):
        v = re.sub(r"http://127\.0\.0\.1:\d+", "<fake>", v)
        return re.sub(r"\?v=\d+", "?v=*", v)
    if isinstance(v, dict):
        return {k: (bool(x) if k in ("cover_v", "checked", "updated") else norm(x)) for k, x in v.items()}
    if isinstance(v, list):
        return [norm(x) for x in v]
    return v


HDRS = ("Content-Type", "Cache-Control", "Access-Control-Allow-Origin", "Access-Control-Allow-Methods",
        "Access-Control-Allow-Headers", "Access-Control-Max-Age", "Vary", "Allow")
fails = []
checks = 0


def compare(label, method, path, body=None, headers=None, raw=None, show=False):
    global checks
    out = {}
    for name in servers:
        code, h, content = req(name, method, path, body, headers, raw)
        hd = {k: h.get(k) for k in HDRS if h.get(k)}
        if "Allow" in hd:  # Flask's order changes from run to run
            hd["Allow"] = sorted(x.strip() for x in hd["Allow"].split(","))
        if "json" in (h.get("Content-Type") or ""):
            payload = norm(json.loads(content)) if content.strip() else None
        elif (h.get("Content-Type") or "").startswith("text/html") and code >= 400:
            payload = content.decode()
        else:
            payload = hashlib.sha1(content).hexdigest() + f" ({len(content)} bytes)"
        out[name] = (code, hd, payload)
    checks += 1
    a, b = out["py"], out["go"]
    if a[0] != b[0] or a[2] != b[2]:
        fails.append((label, a, b))
        print(f"DIFF {label}\n  py: {a}\n  go: {b}")
    elif a[1] != b[1]:
        fails.append((label + " [headers]", a[1], b[1]))
        print(f"DIFF headers {label}\n  py: {a[1]}\n  go: {b[1]}")
    elif show:
        print(f"ok {label}: {a[0]} {str(a[2])[:200]}")
    return a


def discs(name):
    return json.loads(req(name, "GET", "/api/discs")[2])["discs"]


def settle(timeout=120):
    """Wait until the background lookups of both servers are done (the listings stop changing)."""
    last, stable, t0 = None, 0, time.time()
    while time.time() - t0 < timeout:
        cur = {n: norm(discs(n)) for n in servers}
        metas = {n: len(os.listdir(f"{servers[n]['data']}/meta")) if os.path.isdir(f"{servers[n]['data']}/meta") else 0
                 for n in servers}
        want = len(cur["py"])  # every disc has its lookup stored, hit or miss
        if cur == last and metas["py"] == metas["go"] == want:
            stable += 1
            if stable >= 3:
                return
        else:
            stable = 0
        last = cur
        time.sleep(1.5)
    print("settle: timeout")


ids = {d["title"]: d["id"] for d in discs("py")}
settle(180)
ids = {d["rel"]: d["id"] for d in discs("py")}
print(len(ids), "discs:", sorted(ids))

# ------------------------------------------------------------------------------- the listing and covers
compare("healthz", "GET", "/healthz")
compare("discs", "GET", "/api/discs")
for d in discs("go"):
    print(f"  go: {d['rel'][:40]:40} -> {d['title']!r} | {d['synopsis'][:30]!r} | {d['genres']} | season={d['season']}")
for rel, i in sorted(ids.items()):
    compare(f"cover {rel}", "GET", f"/api/cover/{i}")
compare("cover missing", "GET", "/api/cover/nope")
compare("static app.js", "GET", "/static/app.js")
compare("static icon", "GET", "/static/icons/icon.svg")
compare("static missing", "GET", "/static/nope.js")
compare("static dir", "GET", "/static/icons")
compare("index", "GET", "/")
compare("player page", "GET", "/player")
compare("manifest", "GET", "/manifest.webmanifest")
compare("favicon", "GET", "/favicon.ico")
compare("unknown route", "GET", "/api/nope")
compare("trailing slash", "GET", "/api/discs/")
compare("wrong method", "DELETE", "/api/discs")
compare("cors get null", "GET", "/api/discs", headers={"Origin": "null"})
compare("cors get other", "GET", "/healthz", headers={"Origin": "http://evil"})
compare("cors preflight", "OPTIONS", "/api/disc/x/meta", headers={"Origin": "http://tv.local",
                                                                   "Access-Control-Request-Method": "PUT"})
compare("cors preflight cover", "OPTIONS", f"/api/disc/{ids['Titanic Disc 1.iso']}/cover", headers={"Origin": "null"})
compare("options unknown", "OPTIONS", "/api/nope", headers={"Origin": "null"})
compare("cors on 404", "GET", "/api/cover/nope", headers={"Origin": "null"})

# ------------------------------------------------------------------------------- edits
t1, a1 = ids["Titanic Disc 1.iso"], ids["AMELIE_POULAIN.iso"]
f2 = ids["Friends S02 D1.iso"]
for label, payload in [("meta title", {"title": "  Zebra ", "year": "1997", "genres": "Drama, drama ,Ação"}),
                       ("meta bad year", {"year": "abc"}), ("meta year range", {"year": 3000}),
                       ("meta not dict", [1, 2]), ("meta empty", {}), ("meta all", {"title": "T", "sort_title": "S",
                        "year": 2000.9, "genres": ["a", "B", "b", 7], "synopsis": " s ", "season": "2", "disc": 1,
                        "watched": 1, "cover_fill": False})]:
    compare(label, "PUT", f"/api/disc/{t1}/meta", body=payload)
compare("meta not json", "PUT", f"/api/disc/{t1}/meta", raw=b"{oops", headers={"Content-Type": "application/json"})
compare("meta text/plain", "PUT", f"/api/disc/{t1}/meta", raw=b"{}", headers={"Content-Type": "text/plain"})
compare("meta missing disc", "PUT", "/api/disc/nope/meta", body={})
settle()
compare("meta season change", "PUT", f"/api/disc/{f2}/meta", body={"season": 1, "disc": 3})
settle()
compare("discs after edits", "GET", "/api/discs")
compare("watched on", "POST", f"/api/disc/{a1}/watched", body={"watched": True})
compare("watched off", "POST", f"/api/disc/{a1}/watched", body={"watched": 0})
compare("watched no body", "POST", f"/api/disc/{ids['lower.ISO']}/watched")
compare("watched on 2", "POST", f"/api/disc/{ids['lower.ISO']}/watched", body={"watched": "yes"})
compare("resume clear", "POST", f"/api/disc/{ids['Titanic Disc 2.iso']}/resume/clear")
compare("resume clear none", "POST", "/api/disc/nope/resume/clear")

# ------------------------------------------------------------------------------- lookups and posters
compare("lookup dvd query", "POST", f"/api/disc/{ids['Unknown Film.iso']}/lookup", body={"query": "Titanic (1953)"})
compare("lookup dvd miss", "POST", f"/api/disc/{ids['Brazil Film.iso']}/lookup", body={"query": "nothing at all"})
compare("lookup dvd null query", "POST", f"/api/disc/{ids['Brazil Film.iso']}/lookup", body={"query": None})
compare("lookup dvd again", "POST", f"/api/disc/{ids['Brazil Film.iso']}/lookup", body={})
compare("lookup season query", "POST", f"/api/disc/{ids['Pocket Show Special Episode Title.iso']}/lookup",
        body={"query": "Friends (1994) S01 D2"})
cd1 = ids["Album One.cue"]
compare("lookup cd", "POST", f"/api/disc/{cd1}/lookup", body={"query": ""})
compare("lookup wave cd", "POST", f"/api/disc/{ids['Wave Album.cue']}/lookup", body={"query": "Wave Album"})
compare("posters", "GET", f"/api/disc/{t1}/posters")
compare("posters query", "GET", f"/api/disc/{t1}/posters?query=Titanic%20(1997)%20Disc%202")
compare("posters one", "GET", f"/api/disc/{t1}/posters?tmdb_kind=movie&tmdb_id=0597")
compare("posters season", "GET", f"/api/disc/{ids['Friends S01 D1.iso']}/posters")
compare("posters season 2", "GET", f"/api/disc/{f2}/posters?tmdb_kind=tv&tmdb_id=1668")
compare("posters bad", "GET", f"/api/disc/{t1}/posters?tmdb_kind=x&tmdb_id=1")
compare("posters bad id", "GET", f"/api/disc/{t1}/posters?tmdb_id=12a")
compare("posters cd", "GET", f"/api/disc/{cd1}/posters")
compare("poster same", "POST", f"/api/disc/{t1}/poster", body={"poster_path": "/t97b.jpg", "tmdb_kind": "movie", "tmdb_id": 597})
compare("poster other", "POST", f"/api/disc/{t1}/poster", body={"poster_path": "/t53.jpg", "tmdb_kind": "movie", "tmdb_id": 16535})
compare("poster tv", "POST", f"/api/disc/{ids['Brazil Film.iso']}/poster", body={"poster_path": "/fr.jpg", "tmdb_kind": "tv", "tmdb_id": 1668})
for bad in [{"poster_path": "../x.jpg", "tmdb_kind": "movie", "tmdb_id": 1}, {"poster_path": "/a.jpg", "tmdb_kind": "x", "tmdb_id": 1},
            {"poster_path": "/a.jpg", "tmdb_kind": "tv", "tmdb_id": "1"}, {"poster_path": "/a.jpg", "tmdb_kind": "tv", "tmdb_id": True},
            {"poster_path": "/a.jpg", "tmdb_kind": "tv", "tmdb_id": 1.0}, {"poster_path": "/a.jpg", "tmdb_kind": "tv", "tmdb_id": 0}, {}]:
    compare(f"poster bad {bad}", "POST", f"/api/disc/{t1}/poster", body=bad)
compare("poster missing image", "POST", f"/api/disc/{t1}/poster", body={"poster_path": "/missing.png", "tmdb_kind": "movie", "tmdb_id": 404})
settle()
compare("discs after lookups", "GET", "/api/discs")

# ------------------------------------------------------------------------------- uploads
png = b"\x89PNG\r\n\x1a\n" + b"x" * 100


def multipart(field, data, fname="c.png"):
    b = "----x123"
    body = (f"--{b}\r\nContent-Disposition: form-data; name=\"{field}\"; filename=\"{fname}\"\r\n"
            f"Content-Type: application/octet-stream\r\n\r\n").encode() + data + f"\r\n--{b}--\r\n".encode()
    return body, {"Content-Type": f"multipart/form-data; boundary={b}"}


for label, (raw, h) in [("upload png", multipart("cover", png)), ("upload bad", multipart("cover", b"GIF89a")),
                        ("upload field", multipart("other", png)), ("upload big", multipart("cover", b"\xff\xd8\xff" + b"x" * (8 * 1024 * 1024)))]:
    compare(label, "POST", f"/api/disc/{a1}/cover", raw=raw, headers=h)
compare("upload no multipart", "POST", f"/api/disc/{a1}/cover", body={})
compare("cover custom", "GET", f"/api/cover/{a1}")
compare("discs custom", "GET", "/api/discs")
compare("cover delete", "DELETE", f"/api/disc/{a1}/cover")
compare("cover after delete", "GET", f"/api/cover/{a1}")

# ------------------------------------------------------------------------------- playback
def vlc_all(**kw):
    for s in servers.values():
        s["vlc"].__dict__.update(kw)
        s["vlc"].log.clear()


def vlc_logs(label):
    global checks
    checks += 1
    a, b = servers["py"]["vlc"].log, servers["go"]["vlc"].log
    if a != b:
        fails.append((label + " [vlc]", a, b))
        print(f"DIFF vlc {label}\n  py: {a}\n  go: {b}")


vlc_all(state="playing", time=1234, length=6000)
compare("status idle", "GET", "/api/player/status")
compare("cmd nothing playing", "POST", "/api/player/cmd", body={"action": "toggle"})
compare("player idle", "GET", "/api/player", headers={"Host": "Example.LAN:8080"})
compare("play resume", "POST", f"/api/play/{t1}", body={"resume": True})
compare("player resume", "GET", "/api/player", headers={"Host": "[::1]:8080"})
compare("status while waiting", "GET", "/api/player/status")
compare("cmd resume_apply", "POST", "/api/player/cmd", body={"action": "resume_apply"})
compare("cmd resume_apply again", "POST", "/api/player/cmd", body={"action": "resume_apply"})
compare("cmd start", "POST", "/api/player/cmd", body={"action": "start"})
vlc_logs("resume and start")
compare("status saves", "GET", "/api/player/status")
compare("discs resume", "GET", "/api/discs")
for body in [{"action": "toggle"}, {"action": "seek", "value": 99.9}, {"action": "seek", "value": -5}, {"action": "seek", "value": 1e300},
             {"action": "seek", "value": "1"}, {"action": "seek", "value": True}, {"action": "seek"}, {"action": "volume", "value": 55},
             {"action": "volume", "value": 500}, {"action": "audio", "value": 2}, {"action": "subtitle", "value": -1},
             {"action": "chapter_next"}, {"action": "chapter_prev"}, {"action": "menu"}, {"action": "return"},
             {"action": "nav", "value": "up"}, {"action": "nav", "value": "sideways"}, {"action": "nav"},
             {"action": "track_next"}, {"action": "dance"}, {"action": None}, {}, [1]]:
    compare(f"cmd {body}", "POST", "/api/player/cmd", body=body)
vlc_logs("dvd commands")
compare("cmd text", "POST", "/api/player/cmd", raw=b"toggle", headers={"Content-Type": "text/plain"})
vlc_all(state="paused", time=0, length=6000)
compare("play over", "POST", f"/api/play/{t1}", body={})
compare("player over", "GET", "/api/player")
compare("cmd start paused", "POST", "/api/player/cmd", body={"action": "start"})
vlc_logs("start paused")
vlc_all(state="playing", time=5990, length=6000)
compare("status at end", "GET", "/api/player/status")
compare("discs end", "GET", "/api/discs")
vlc_all(state="playing", time=700, length=100)
compare("status short", "GET", "/api/player/status")

vlc_all(state="playing", time=12, length=20)
compare("play cd", "POST", f"/api/play/{cd1}", body={"resume": True})
compare("player cd", "GET", "/api/player")
compare("cd resume_apply", "POST", "/api/player/cmd", body={"action": "resume_apply"})
compare("cd start", "POST", "/api/player/cmd", body={"action": "start"})
compare("cd status", "GET", "/api/player/status")
for body in [{"action": "seek", "value": 3}, {"action": "track_next"}, {"action": "chapter_prev"}, {"action": "track", "value": 2},
             {"action": "track", "value": 99}, {"action": "track"}, {"action": "toggle"}, {"action": "volume", "value": 10}]:
    compare(f"cd cmd {body}", "POST", "/api/player/cmd", body=body)
vlc_all(state="playing", time=16, length=20)
compare("cd track_next last", "POST", "/api/player/cmd", body={"action": "track_next"})
compare("cd track_prev", "POST", "/api/player/cmd", body={"action": "track_prev"})
vlc_logs("cd commands")
compare("discs cd resume", "GET", "/api/discs")
wave = ids["Wave Album.cue"]
vlc_all(state="playing", time=75, length=130)
compare("play wave", "POST", f"/api/play/{wave}")
compare("wave start", "POST", "/api/player/cmd", body={"action": "start"})
compare("wave status", "GET", "/api/player/status")
vlc_logs("wave")

for s in servers.values():  # VLC gone: offline / 503
    s["vlc"].sock.close()
compare("status offline", "GET", "/api/player/status")
compare("cmd offline", "POST", "/api/player/cmd", body={"action": "toggle"})
compare("stop", "POST", "/api/stop")
compare("status after stop", "GET", "/api/player/status")
compare("player after stop", "GET", "/api/player")
compare("play missing", "POST", "/api/play/nope")
compare("rescan", "POST", "/api/rescan")
settle()
compare("discs final", "GET", "/api/discs")


# ------------------------------------------------------------------------------- the files they wrote
def files(name):
    out = {}
    root = servers[name]["data"]
    for dp, _, fns in os.walk(root):
        for fn in fns:
            p = os.path.join(dp, fn)
            rel = os.path.relpath(p, root)
            data = open(p, "rb").read()
            if fn.endswith(".json"):
                out[rel] = norm(json.loads(data))
            else:
                out[rel] = hashlib.sha1(data).hexdigest()
    st = servers[name]["state"]
    out["<state>"] = open(st).read() if os.path.exists(st) else None
    return out


fa, fb = files("py"), files("go")
for k in sorted(set(fa) | set(fb)):
    checks += 1
    if fa.get(k) != fb.get(k):
        fails.append((f"file {k}", fa.get(k), fb.get(k)))
        print(f"DIFF file {k}\n  py: {fa.get(k)}\n  go: {fb.get(k)}")
checks += 1
ca = [c for c in servers["py"]["fake"].calls]
cb = [c for c in servers["go"]["fake"].calls]
if sorted(map(str, ca)) != sorted(map(str, cb)):
    fails.append(("upstream calls", len(ca), len(cb)))
    sa, sb = sorted(map(str, ca)), sorted(map(str, cb))
    d1 = collections.Counter(sa) - collections.Counter(sb)
    d2 = collections.Counter(sb) - collections.Counter(sa)
    print("DIFF upstream calls: more in py:", list(d1.items())[:10], "more in go:", list(d2.items())[:10])

for s in servers.values():
    s["proc"].terminate()
print(f"\n{checks} checks, {len(fails)} differences")
sys.exit(1 if fails else 0)
