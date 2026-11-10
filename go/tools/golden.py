# Writes go/testdata/golden.json: what the Python catalog (../catalog) answers for many inputs, so
# the Go version can be checked against it with go test. Run it in the catalog image, from the repo:
#   docker build -t streamiso-catalog catalog
#   docker run --rm -v "$PWD:/w" streamiso-catalog python -I /w/go/tools/golden.py [library.json]
# The optional argument is a saved GET /api/discs, whose titles are checked too (keep that run's
# golden.json to yourself: it holds the names of your discs).
import json, os, sys, textwrap
os.environ.setdefault("DATA_DIR", "/tmp/golden-data")
sys.path.insert(0, "/w/catalog")
import app as A
import musicbrainz as MB

live = json.load(open(sys.argv[1], encoding="utf-8"))["discs"] if len(sys.argv) > 1 else []
titles = set()
for d in live:
    for v in (d["title"], d["rel"], d["rel"].rsplit("/", 1)[-1].rsplit(".", 1)[0], d["label"], d["auto"]["title"],
              d["custom"].get("title") or ""):
        titles.add(v)
extra = """Friends S01 D02|Lost - Season 2 Disc 3|Dexter T1 Disco 4|T2 Trainspotting|Titanic Disc 2|Lawrence of Arabia - DVD 1|Ben-Hur D2
Gandhi (Disco 1 de 2)|Gandhi (Disc 1 of 2)|Film [Disc 1/2]|Show S1E2|Show s01d1|Show S1D1|Show S01 Disk 3|Show Season 03 Disc 4
S01 Friends|Season 2|Disc 2|Friends S0|Friends S00 D1|Friends S000|Friends S0001|Friends S1234|X - S2|X: Temporada 3
Saison 2 Episode|Staffel 4 Teil 2|The Office (US) S03 D2|Will & Grace (2017) S01 D2|SOME_SHOW_S1_D1|FILM_X|FILM_NAME
Dr. No|Mr. & Mrs. Smith|Guarda-Chuva Voador Muito Comprido Mesmo|Supercalifragilisticexpialidocious Mega|A-B-C-D-E-F-G-H-I-J
a  b   c|  leading spaces|trailing -|- dash -|VIDEO_TS|DVD|DVD_VIDEO|disco|movie (1999)|[TAG] Name (2001) [x264]
CARTOON SHOW WHERE ARE YOU S2 D1|Concert Special 25Th|25TH ANNIVERSARY|ÉLITE BRASILEIRA|ação e reação|NAÏVE CAFÉ
straße|ǅemal|İstanbul|tab\tseparated|em–dash – test|Show — Special|cd1|CD 2|Album CD1|Album D 3|Album dvd1|Album Disco2
Film d1 of 2|Film disc 1de2|Film disc 1 / 2|Film (Disc1)|Film, Disc 1|Film:Disc 1|Film-D1|Film D12|Film D123|Film D01
O Bicho-Papão|Turma da Praça: A Ilha Misteriosa|1917|2001: A Space Odyssey|Se7en|Ocean's Eleven|Amélie|Léon: The Professional
x|xy|abcdefghijklmn|abcdefghijklmno|abc defghijklmnop|Twelve Chars Exactly Here Now And Some""".replace("\n", "|").split("|")
titles.update(extra)
titles = sorted(titles)

def tup(x):
    return list(x) if x else None

out = {"titles": []}
for t in titles:
    out["titles"].append({
        "in": t,
        "clean_title": list(A.clean_title(t)),
        "is_generic": A.is_generic(t),
        "parse_season": tup(A.parse_season(t)),
        "parse_film_disc": tup(A.parse_film_disc(t)),
        "film_name": A.film_name(t),
        "words": A._words(t),
        "title_variants": [list(v) for v in A.title_variants(t)],
        "wrap": textwrap.wrap(t, 14),
        "title": t.title(),
        "svg": A.placeholder_svg(t, 1999 if len(t) % 2 else None, square=len(t) % 3 == 0),
        "mb_variants": [list(v) for v in MB._variants(t)],
        "mb_norm": MB._norm(t),
        "casefold": t.casefold(),
    })

pairs = [("Show Name", "Show Name Special"), ("Will Grace", "Will & Grace 2017"), ("a b", "a b"), ("", "x"),
         ("x", ""), ("Os Simpsons", "Simpsons"), ("Simpsons", "Os Simpsons 1999"), ("1999", "1999 x")]
for d in live[:30]:
    pairs.append((d["auto"]["title"], d["title"]))
out["is_part_of"] = [[a, b, A.is_part_of(a, b)] for a, b in pairs]

loc = []
metas = [{"tmdb_title": "Amigos", "whole": True}, {"tmdb_title": "Amigos", "matched": "x y z"},
         {"tmdb_title": "Amigos", "whole": False}, {"tmdb_title": "", "whole": True}, {}, {"tmdb_title": "Y", "matched": "(chosen by hand)"}]
for t in titles[:150]:
    for m in metas:
        for kind in ("dvd", "cd"):
            loc.append([t, kind, m, A.localized_title({"title": t, "kind": kind}, m)])
out["localized"] = loc

payloads = [{}, {"title": "  x  "}, {"title": 5}, {"title": None}, {"title": True}, {"title": 5.0}, {"year": "2001"},
            {"year": " 2001 "}, {"year": "20_01"}, {"year": "2001.5"}, {"year": 2001.7}, {"year": 1800}, {"year": True},
            {"year": []}, {"year": ""}, {"genres": "a, b,A ,c"}, {"genres": ["x", "X", " y ", 3, None]},
            {"genres": {"a": 1}}, {"genres": [str(i) for i in range(20)]}, {"season": 0}, {"season": -1},
            {"season": "1000"}, {"disc": 0}, {"disc": 3}, {"watched": 1}, {"watched": 0}, {"watched": "no"},
            {"cover_fill": False}, {"cover_fill": 0}, {"cover_fill": True}, {"synopsis": "x" * 5000},
            {"title": "é" * 250}, {"sort_title": "  A  "}, {"query": None}]
cm = []
for p in payloads:
    try:
        cm.append([p, A.clean_meta(p), None])
    except ValueError as e:
        cm.append([p, None, str(e)])
out["clean_meta"] = cm

out["round"] = [[x, round(x), round(x, 2)] for x in [0.5, 1.5, 2.5, -0.5, 2.675, 1.005, 183.465, 0.125, 99.995, 3.14159]]
json.dump(out, open("/w/go/testdata/golden.json", "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print(len(titles), "titles,", len(loc), "localized,", len(cm), "payloads")
