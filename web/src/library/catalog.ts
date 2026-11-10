// The library's rules, with no DOM: seasons and films on several discs, sorting, filtering, genre tags and
// the texts on the cards. Season and disc numbers must be read as the catalog reads them (ParseSeason and
// ParseFilmDisc in catalog/internal/domain/titles.go).

import type { Disc, Kind } from "../shared/types";
import { fmtTime } from "../shared/format";

export interface Prefs {
  sort: SortKey;
  genre: string;
  kind: "" | Kind;
  /** Leave out the discs already watched / listened to (the "Hide watched" switch, on by default). */
  hideSeen: boolean;
}

export type SortKey = "title" | "year-desc" | "year-asc" | "added" | "series";

/** A season, or a film on several discs (season null). */
export interface SeasonInfo {
  key: string;
  name: string;
  season: number | null;
  disc: number | null | undefined;
}

/** Several discs shown as one card. */
export interface Group {
  key: string;
  name: string;
  season: number | null;
  discs: Disc[];
}

export type Item = Disc | Group;

export function isGroup(item: Item): item is Group {
  return (item as Group).discs !== undefined;
}

export const DEFAULT_GENRES = [
  "Action", "Adventure", "Animation", "Comedy", "Concert", "Crime", "Documentary", "Drama",
  "Family", "Fantasy", "History", "Horror", "Music", "Mystery", "Romance",
  "Science Fiction", "Thriller", "War", "Western",
];

export function cmpText(a: unknown, b: unknown): number {
  return String(a).localeCompare(String(b), "en", { sensitivity: "base", numeric: true });
}

function num(v: number | null | undefined, fallback: number): number {
  return v === null || v === undefined ? fallback : v;
}

const given = (v: unknown): v is number => v !== null && v !== undefined;

// ------------------------------------------------------------------------- seasons

// Season / disc numbers written in the title, e.g. "Friends S01 D02", "Lost - Season 2 Disc 3",
// "Dexter T1 Disco 4". The series name is what comes before the season.
const SEASON_RE = /(?:^|[\s\-–:,(])(?:season|temporada|saison|staffel|s|t)\s*0*(\d{1,3})(?=$|[\s\-–:,)]|d(?:is[ck]o?)?\s*\d|e\d)/i;
const DISC_RE = /(?:^|[\s\-–:,(\d])(?:dis[ck]o?|dvd|d)\s*0*(\d{1,3})(?=$|[^\d])/i;
// Disc number of a film on several discs, e.g. "Titanic Disc 2", "Lawrence of Arabia - DVD 1",
// "Ben-Hur D2", "Gandhi (Disco 1 de 2)".
const FILM_DISC_RE = /(?:^|[\s\-–:,(\[])(?:dis[ck]o?|dvd|cd|d)\s*0*(\d{1,2})(?=$|[\s\-–:,)\]]|\s*(?:of|de|\/)\s*\d)/i;

export function parseFilmDisc(title: string | null | undefined): { name: string; disc: number } | null {
  const t = String(title || "");
  const m = FILM_DISC_RE.exec(t);
  if (!m) return null;
  const name = t.slice(0, m.index).replace(/[\s\-–:,(\[]+$/, "").trim();
  return name ? { name, disc: Number(m[1]) } : null;
}

export function parseTitle(title: string | null | undefined): { name: string; season: number; disc: number | null } | null {
  const t = String(title || "");
  const m = SEASON_RE.exec(t);
  if (!m) return null;
  const name = t.slice(0, m.index).replace(/[\s\-–:,(]+$/, "").trim();
  const rest = t.slice(m.index + m[0].length);
  const dm = DISC_RE.exec(rest);
  return { name, season: Number(m[1]), disc: dm ? Number(dm[1]) : null };
}

/**
 * The season a DVD belongs to, from its edited season, its title, or (when the title says nothing) its
 * file name / volume label. The series name is the title, minus any "S02 D1" in it. Discs matched to a TV
 * series on TMDB are grouped by that series, so the seasons of a revival ("Will & Grace", 2017) stay apart
 * from the original's. Without a season, a DVD with a disc number (edited, or "Titanic Disc 2" in its title,
 * file name or volume label) is a film on several discs: season is null and the discs of the same film name
 * form one group. Otherwise null.
 */
export function seasonOf(d: Disc): SeasonInfo | null {
  if (d.kind === "cd") return null;
  const file = String(d.rel || "").replace(/^.*\//, "").replace(/\.[^.]*$/, "").replace(/[_.]+/g, " ");
  const label = String(d.label || "").replace(/[_.]+/g, " ");
  const fromTitle = parseTitle(d.title);
  const p: { season?: number; disc?: number | null } = fromTitle || parseTitle(file) || parseTitle(label) || {};
  let name = (fromTitle && fromTitle.name) || d.title;
  const season = given(d.season) ? d.season : p.season;
  if (!name) return null;
  if (!given(season)) {
    const film = parseFilmDisc(d.title);
    const f: { disc?: number } = film || parseFilmDisc(file) || parseFilmDisc(label) || {};
    const disc = given(d.disc) ? d.disc : f.disc;
    if (!given(disc)) return null;
    name = (film && film.name) || d.title;
    return { key: "film:" + name.toLowerCase(), name, season: null, disc };
  }
  const series = d.tmdb_series ? "tmdb:" + d.tmdb_series : name.toLowerCase();
  return { key: series + "|" + season, name, season, disc: given(d.disc) ? d.disc : p.disc };
}

/** "Friends – Season 2", or just the name of a film on several discs. */
export function groupHeading(s: { name: string; season: number | null }): string {
  return s.season === null ? s.name : s.name + " – Season " + s.season;
}

export function byDiscNumber(a: Disc, b: Disc): number {
  const sa = seasonOf(a);
  const sb = seasonOf(b);
  return num(sa && sa.disc, 9999) - num(sb && sb.disc, 9999) || cmpText(a.title, b.title);
}

/**
 * Replaces the discs of a season that appear more than once by a single group, kept at the place of its
 * first disc in the current sort order.
 */
export function collapse(items: Disc[]): Item[] {
  const groups: Record<string, Group> = {};
  for (const d of items) {
    const s = seasonOf(d);
    if (!s) continue;
    (groups[s.key] = groups[s.key] || { key: s.key, name: s.name, season: s.season, discs: [] }).discs.push(d);
  }
  const out: Item[] = [];
  const placed: Record<string, boolean> = {};
  for (const d of items) {
    const s = seasonOf(d);
    const g = s && groups[s.key];
    if (!g || g.discs.length < 2) {
      out.push(d);
      continue;
    }
    if (placed[g.key]) continue;
    placed[g.key] = true;
    g.discs.sort(byDiscNumber);
    out.push(g);
  }
  return out;
}

// ------------------------------------------------------------------------- sorting and filtering

/** The sort title set in the editor, or else the title shown. */
function byTitle(a: Disc, b: Disc): number {
  return cmpText(a.sort_title || a.title, b.sort_title || b.title) || cmpText(a.title, b.title);
}

export const sorters: Record<SortKey, (a: Disc, b: Disc) => number> = {
  title: byTitle,
  "year-desc": (a, b) => num(b.year, -1) - num(a.year, -1) || byTitle(a, b),
  "year-asc": (a, b) => num(a.year, 99999) - num(b.year, 99999) || byTitle(a, b),
  added: (a, b) => b.added - a.added || byTitle(a, b),
  series: (a, b) => {
    let sa = seasonOf(a);
    let sb = seasonOf(b);
    if (sa && sa.season === null) sa = null; // a film on several discs sorts by title
    if (sb && sb.season === null) sb = null;
    if (!sa !== !sb) return sa ? -1 : 1; // discs of a season first
    if (!sa || !sb) return byTitle(a, b) || byDiscNumber(a, b);
    return (
      cmpText(sa.name, sb.name) ||
      num(a.tmdb_series, 0) - num(b.tmdb_series, 0) || // same-named series (a revival) apart
      (sa.season as number) - (sb.season as number) ||
      num(sa.disc, 0) - num(sb.disc, 0) ||
      cmpText(a.title, b.title)
    );
  },
};

/** The tag a genre is listed under: its name in the app's language ("Documentary" is "Documentário"). */
export function genreTag(aliases: Record<string, string>, g: string): string {
  return Object.prototype.hasOwnProperty.call(aliases, g) ? aliases[g] : g;
}

/** A disc's genres as tags, without repeats ("Comedy" and "Comédia" are one tag). */
export function discTags(aliases: Record<string, string>, d: Disc): string[] {
  const seen: Record<string, boolean> = {};
  return d.genres.map((g) => genreTag(aliases, g)).filter((g) => {
    const k = g.toLowerCase();
    if (seen[k]) return false;
    seen[k] = true;
    return true;
  });
}

/** The discs that pass the filters, in the chosen order. */
export function visibleList(discs: Disc[], prefs: Prefs, filter: string, aliases: Record<string, string>): Disc[] {
  const q = filter.trim().toLowerCase();
  const list = discs.filter((d) => {
    if (prefs.genre && discTags(aliases, d).indexOf(prefs.genre) === -1) return false;
    if (prefs.kind && d.kind !== prefs.kind) return false;
    if (q) {
      const hay = (d.title + " " + (d.sort_title || "") + " " + (d.artist || "") + " " +
        d.genres.concat(discTags(aliases, d)).join(" ")).toLowerCase();
      if (hay.indexOf(q) === -1) return false;
    }
    return true;
  });
  list.sort(sorters[prefs.sort] || sorters.title);
  return list;
}

// ------------------------------------------------------------------------- texts

/** "watched" for DVDs, "listened" for audio CDs. */
export function seenWord(d: Disc): string {
  return d.kind === "cd" ? "listened" : "watched";
}

export function cap(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** "Continue at 1:23:10" for a DVD, "Continue: track 7 · 1:02" for a CD. */
export function resumeText(d: Disc): string {
  const r = d.resume;
  if (!r) return "";
  return d.kind === "cd" ? "Continue: track " + r.track + " · " + fmtTime(r.track_time) : "Continue at " + fmtTime(r.time);
}

/** The line under a card's title. */
export function subText(d: Disc): string {
  const parts: (string | number)[] = [];
  if (d.kind === "cd") {
    if (d.artist) parts.push(d.artist);
    if (d.year) parts.push(d.year);
    parts.push(d.track_count + (d.track_count === 1 ? " track" : " tracks"));
    return parts.join(" · ");
  }
  if (d.year) parts.push(d.year);
  const st: string[] = [];
  if (given(d.season)) st.push("S" + d.season);
  if (given(d.disc)) st.push("D" + d.disc);
  if (st.length) parts.push(st.join(" "));
  return parts.join(" · ");
}

/** Hidden by the eye button: a disc already watched / listened to, or a group whose discs all are. */
export function seenHidden(item: Item, prefs: Prefs): boolean {
  if (!prefs.hideSeen) return false;
  return isGroup(item) ? item.discs.every((d) => d.watched) : !!item.watched;
}

/** Number of discs behind the cards (a group stands for several). */
export function discCount(items: Item[]): number {
  return items.reduce((n, it) => n + (isGroup(it) ? it.discs.length : 1), 0);
}

/** The other discs grouped with this one (same season, or same film on several discs). */
export function groupSiblings(discs: Disc[], d: Disc): Disc[] {
  const s = seasonOf(d);
  if (!s) return [];
  return discs.filter((o) => {
    const so = o.id !== d.id && seasonOf(o);
    return so && so.key === s.key;
  });
}

export interface Section {
  title: string;
  items: Item[];
  progress: boolean;
}

export interface Layout {
  sections: Section[];
  /** The count next to the logo. */
  count: string;
  /** Shown when no card is: why. */
  empty: "none" | "no-match" | "all-seen" | null;
}

/**
 * The library as shown: discs in progress first, in a section of their own (even when already watched; one
 * that belongs to a season also stays in its group's card), then one section per kind.
 */
export function layout(all: Disc[], prefs: Prefs, filter: string, aliases: Record<string, string>): Layout {
  const list = visibleList(all, prefs, filter, aliases);
  const progress = list.filter((d) => d.resume);
  const grouped: Record<string, boolean> = {};
  for (const it of collapse(list)) if (isGroup(it)) for (const d of it.discs) grouped[d.id] = true;
  const rest = list.filter((d) => !d.resume || grouped[d.id]);

  const sections: Section[] = [];
  if (progress.length) {
    const onlyCd = progress.every((d) => d.kind === "cd");
    const anyCd = progress.some((d) => d.kind === "cd");
    sections.push({ title: onlyCd ? "Continue listening" : anyCd ? "Continue" : "Continue watching", items: progress, progress: true });
  }
  // DVDs and CDs are not mixed. Headings only matter when both exist, or under the "Continue" section.
  const kinds: Record<string, boolean> = {};
  for (const d of all) kinds[d.kind] = true;
  const showHeadings = !!(kinds.dvd && kinds.cd) || progress.length > 0;
  let shown = progress.filter((d) => !grouped[d.id]).length; // the others count in their group
  let hiddenSeen = 0;
  for (const [kind, heading] of [["dvd", "DVDs"], ["cd", "Music CDs"]] as const) {
    const items = collapse(rest.filter((d) => d.kind === kind));
    const visible = items.filter((it) => !seenHidden(it, prefs));
    hiddenSeen += discCount(items) - discCount(visible);
    shown += discCount(visible);
    if (visible.length) sections.push({ title: showHeadings ? heading : "", items: visible, progress: false });
  }

  let count = "";
  if (all.length) count = shown === all.length ? all.length + (all.length === 1 ? " disc" : " discs") : shown + " of " + all.length;

  let empty: Layout["empty"] = null;
  if (!all.length) empty = "none";
  else if (!list.length) empty = "no-match";
  else if (!sections.length && hiddenSeen) empty = "all-seen";
  return { sections, count, empty };
}

/** The eye button's texts: "watched" for DVDs, "listened" for CDs, "played" for a mix. */
export function seenLabels(all: Disc[], prefs: Prefs): { text: string; title: string; aria: string } {
  const hasCd = all.some((d) => d.kind === "cd");
  const word = prefs.kind === "cd" ? "listened" : prefs.kind === "dvd" || !hasCd ? "watched" : "played";
  // says what is on screen: "Hide watched" (highlighted) while they are hidden, "Show watched" while shown
  return {
    text: (prefs.hideSeen ? "Hide " : "Show ") + word,
    title: prefs.hideSeen ? "The " + word + " ones are hidden: click to show them" : "The " + word + " ones are shown: click to hide them",
    aria: "Hide " + word,
  };
}
