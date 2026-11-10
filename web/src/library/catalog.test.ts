import { describe, expect, it } from "vitest";
import type { Disc } from "../shared/types";
import { collapse, discTags, isGroup, layout, parseFilmDisc, parseTitle, resumeText, seasonOf, seenLabels, sorters, subText, visibleList } from "./catalog";
import type { Prefs } from "./catalog";

let n = 0;
function disc(over: Partial<Disc>): Disc {
  n++;
  return {
    id: "d" + n, kind: "dvd", artist: "", track_count: 0, duration: null, title: "Film " + n, sort_title: "Film " + n,
    year: null, genres: [], synopsis: "", watched: false, cover_fill: true, resume: null, tmdb_series: null,
    season: null, disc: null, rating: null, label: "", size: 4.7e9, rel: "film" + n + ".iso", added: n, cover_v: 0,
    custom_cover: false, custom: {}, auto: { title: "", year: null, genres: [], synopsis: "" }, ...over,
  };
}

const prefs: Prefs = { sort: "title", genre: "", kind: "", hideSeen: false };

describe("titles", () => {
  it("reads seasons and discs", () => {
    expect(parseTitle("Friends S01 D02")).toEqual({ name: "Friends", season: 1, disc: 2 });
    expect(parseTitle("Lost - Season 2 Disc 3")).toEqual({ name: "Lost", season: 2, disc: 3 });
    expect(parseTitle("Dexter T1 Disco 4")).toEqual({ name: "Dexter", season: 1, disc: 4 });
    expect(parseTitle("Titanic")).toBeNull();
  });
  it("reads films on several discs", () => {
    expect(parseFilmDisc("Titanic Disc 2")).toEqual({ name: "Titanic", disc: 2 });
    expect(parseFilmDisc("Gandhi (Disco 1 de 2)")).toEqual({ name: "Gandhi", disc: 1 });
    expect(parseFilmDisc("Ben-Hur D2")).toEqual({ name: "Ben-Hur", disc: 2 });
    expect(parseFilmDisc("Apollo 13")).toBeNull();
  });
  it("takes the season from the file name when the title has none", () => {
    const s = seasonOf(disc({ title: "Friends", rel: "tv/Friends_S02_D1.iso" }));
    expect(s).toMatchObject({ key: "friends|2", name: "Friends", season: 2, disc: 1 });
  });
  it("keeps a TMDB series apart from a same-named one", () => {
    expect(seasonOf(disc({ title: "Will & Grace S01 D1", tmdb_series: 1234 }))?.key).toBe("tmdb:1234|1");
  });
  it("has no season for CDs", () => {
    expect(seasonOf(disc({ kind: "cd", title: "Album S01" }))).toBeNull();
  });
});

describe("groups", () => {
  it("collapses the discs of a season, in disc order, at the first one's place", () => {
    const a = disc({ title: "Alpha" });
    const d2 = disc({ title: "Friends S01 D2" });
    const d1 = disc({ title: "Friends S01 D1" });
    const out = collapse([a, d2, d1]);
    expect(out.length).toBe(2);
    const g = out[1];
    expect(isGroup(g) && g.discs.map((d) => d.id)).toEqual([d1.id, d2.id]);
  });
  it("leaves a single disc of a season alone", () => {
    const d = disc({ title: "Friends S01 D1" });
    expect(collapse([d])).toEqual([d]);
  });
});

describe("sorting and filtering", () => {
  it("sorts by sort title, numbers in order", () => {
    const list = [disc({ title: "Film 10", sort_title: "Film 10" }), disc({ title: "Film 9", sort_title: "Film 9" }), disc({ title: "B", sort_title: "A" })];
    expect(list.sort(sorters.title).map((d) => d.title)).toEqual(["B", "Film 9", "Film 10"]);
  });
  it("filters by genre tag, also for a genre stored in English", () => {
    const aliases = { Comedy: "Comédia" };
    const a = disc({ genres: ["Comedy"] });
    const b = disc({ genres: ["Drama"] });
    expect(discTags(aliases, a)).toEqual(["Comédia"]);
    expect(visibleList([a, b], { ...prefs, genre: "Comédia" }, "", aliases)).toEqual([a]);
  });
  it("searches titles, artists and genres", () => {
    const a = disc({ kind: "cd", artist: "Nina Simone", title: "Pastel Blues" });
    expect(visibleList([a, disc({})], prefs, "nina", {})).toEqual([a]);
  });
});

describe("layout", () => {
  it("puts discs in progress first and counts the shown ones", () => {
    const going = disc({ title: "Going", resume: { time: 100, pct: 5 } });
    const seen = disc({ title: "Seen", watched: true });
    const l = layout([going, seen, disc({ title: "New" })], { ...prefs, hideSeen: true }, "", {});
    expect(l.sections.map((s) => s.title)).toEqual(["Continue watching", "DVDs"]);
    expect(l.count).toBe("2 of 3");
  });
  it("says why nothing shows", () => {
    expect(layout([], prefs, "", {}).empty).toBe("none");
    expect(layout([disc({})], prefs, "zzz", {}).empty).toBe("no-match");
    expect(layout([disc({ watched: true })], { ...prefs, hideSeen: true }, "", {}).empty).toBe("all-seen");
  });
  it("names the eye button by what the library holds", () => {
    expect(seenLabels([disc({})], { ...prefs, hideSeen: true }).text).toBe("Hide watched");
    expect(seenLabels([disc({}), disc({ kind: "cd" })], prefs).text).toBe("Show played");
  });
});

describe("texts", () => {
  it("writes the line under a title", () => {
    expect(subText(disc({ year: 2004, season: 2, disc: 3 }))).toBe("2004 · S2 D3");
    expect(subText(disc({ kind: "cd", artist: "Art", year: 1999, track_count: 1 }))).toBe("Art · 1999 · 1 track");
  });
  it("writes where a disc continues", () => {
    expect(resumeText(disc({ resume: { time: 4990, pct: 50 } }))).toBe("Continue at 1:23:10");
    expect(resumeText(disc({ kind: "cd", resume: { time: 900, pct: 20, track: 7, track_time: 62 } }))).toBe("Continue: track 7 · 1:02");
  });
});
