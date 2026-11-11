// The answers of the catalog's API (docs/API.md), as the pages use them.

export type Kind = "dvd" | "cd";

/** "bluray" for a Blu-ray image (its kind is "dvd": it plays the same way), else null. */
export type Format = "bluray" | null;

/** Where a disc was left. */
export interface Resume {
  time: number;
  pct: number | null;
  /** CDs: the track (from 1) and the time in it. */
  track?: number;
  track_time?: number;
}

/** The user's own edits of a disc. */
export interface Edits {
  title?: string;
  sort_title?: string;
  year?: number;
  genres?: string[];
  synopsis?: string;
  season?: number;
  disc?: number;
  watched?: boolean;
  cover_fill?: boolean;
}

/** What was found for a disc, before the user's edits. */
export interface AutoInfo {
  title: string;
  year: number | null;
  genres: string[];
  synopsis: string;
}

export interface Disc {
  id: string;
  kind: Kind;
  format?: Format;
  artist: string;
  track_count: number;
  duration: number | null;
  title: string;
  sort_title: string;
  year: number | null;
  genres: string[];
  synopsis: string;
  watched: boolean;
  cover_fill: boolean;
  resume: Resume | null;
  tmdb_series: number | null;
  season: number | null;
  disc: number | null;
  rating: number | null;
  label: string;
  size: number;
  rel: string;
  added: number;
  cover_v: number;
  custom_cover: boolean;
  custom: Edits;
  auto: AutoInfo;
}

export interface LibraryAnswer {
  discs: Disc[];
  genres: string[];
  genre_aliases: Record<string, string>;
  media_dir: string;
  tmdb: boolean;
  playing: string | null;
}

export interface Track {
  n: number;
  title: string;
  artist: string;
  start: number;
  length: number | null;
}

/** GET /api/player: what is open. */
export interface PlayerInfo {
  url: string;
  title: string | null;
  id: string | null;
  kind: Kind;
  format?: Format;
  resume?: boolean;
  resume_at?: number | null;
  artist?: string;
  tracks?: Track[];
  cover?: string;
}

export interface TrackChoice {
  id: number;
  name: string;
  selected: boolean;
}

/** GET /api/player/status. */
export interface PlayerStatus {
  state: string;
  time?: number | null;
  length?: number | null;
  volume?: number | null;
  chapter?: number | null;
  title?: number | null;
  audio?: TrackChoice[];
  subtitles?: TrackChoice[];
  /** CDs */
  kind?: Kind;
  track?: number;
  tracks?: number;
  abs?: number;
}

export interface PosterMatch {
  tmdb_kind: "movie" | "tv";
  tmdb_id: number;
  title: string;
  year: string;
  poster_path: string;
}

export interface PosterChoices {
  query: string;
  current: (Omit<PosterMatch, "year"> & { year: string; season: number | null; posters: string[] }) | null;
  matches: PosterMatch[];
  thumb_url: string;
  covered: boolean;
}
