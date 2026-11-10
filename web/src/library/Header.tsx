// The bar on top: logo and count, filter, sort, genre and type, the eye button, Refresh, and the server
// button of the Android app.
import type { Ref } from "preact";
import type { Prefs, SortKey } from "./catalog";

interface Props {
  count: string;
  prefs: Prefs;
  genres: string[];
  seen: { text: string; title: string; aria: string };
  filter: string;
  filterRef: Ref<HTMLInputElement>;
  rescanning: boolean;
  onFilter(text: string): void;
  onPrefs(change: Partial<Prefs>): void;
  onRescan(): void;
  /** The Android app's "switch server", when the page runs inside it. */
  onServer: (() => void) | null;
}

export function Header(p: Props) {
  return (
    <header>
      <h1 class="brand"><img src="/static/logo.svg" alt="StreamISO" class="logo" /></h1>
      <span id="count" class="muted">{p.count}</span>
      <div class="spacer" />
      <input
        id="filter"
        ref={p.filterRef}
        type="search"
        placeholder="Filter…"
        autocomplete="off"
        value={p.filter}
        onInput={(e) => p.onFilter((e.target as HTMLInputElement).value)}
      />
      <select id="sort" aria-label="Sort by" value={p.prefs.sort} onChange={(e) => p.onPrefs({ sort: (e.target as HTMLSelectElement).value as SortKey })}>
        <option value="title">Title (A–Z)</option>
        <option value="year-desc">Year (newest)</option>
        <option value="year-asc">Year (oldest)</option>
        <option value="added">Recently added</option>
        <option value="series">Series &amp; season</option>
      </select>
      <select id="genre" aria-label="Genre" value={p.prefs.genre} onChange={(e) => p.onPrefs({ genre: (e.target as HTMLSelectElement).value })}>
        <option value="">All genres</option>
        {p.genres.map((g) => <option key={g} value={g}>{g}</option>)}
      </select>
      <select id="kind" aria-label="Type" value={p.prefs.kind} onChange={(e) => p.onPrefs({ kind: (e.target as HTMLSelectElement).value as Prefs["kind"] })}>
        <option value="">All types</option>
        <option value="dvd">DVDs</option>
        <option value="cd">Music CDs</option>
      </select>
      {/* Shows or hides the discs already watched (DVDs) or listened to (CDs); hidden at first. */}
      <button
        id="seen"
        type="button"
        aria-pressed={p.prefs.hideSeen ? "true" : "false"}
        title={p.seen.title}
        aria-label={p.seen.aria}
        onClick={() => p.onPrefs({ hideSeen: !p.prefs.hideSeen })}
      >
        <svg class="eye" viewBox="0 0 24 24" width="22" height="22" aria-hidden="true"><path fill="currentColor" d="M12 5C6.5 5 2.3 8.6 1 12c1.3 3.4 5.5 7 11 7s9.7-3.6 11-7c-1.3-3.4-5.5-7-11-7zm0 11.5a4.5 4.5 0 1 1 0-9 4.5 4.5 0 0 1 0 9zm0-7a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5z" /></svg>
        <svg class="eye-off" viewBox="0 0 24 24" width="22" height="22" aria-hidden="true"><path fill="currentColor" d="M2.3 3.7 3.7 2.3l18 18-1.4 1.4-3.4-3.4A12 12 0 0 1 12 19c-5.5 0-9.7-3.6-11-7a12.6 12.6 0 0 1 4.2-5.3L2.3 3.7zM12 5c5.5 0 9.7 3.6 11 7a12.4 12.4 0 0 1-3.3 4.6l-3.3-3.3A4.5 4.5 0 0 0 10.7 7.6L8.3 5.2C9.5 5.1 10.7 5 12 5zm-4.4 4.1A4.5 4.5 0 0 0 14.9 15.4l-1.5-1.5a2.5 2.5 0 0 1-3.3-3.3L7.6 9.1z" /></svg>
        <span class="lbl">{p.seen.text}</span>
      </button>
      <button id="rescan" type="button" title="Look for new ISO files" aria-label="Refresh" disabled={p.rescanning} onClick={p.onRescan}>
        <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true"><path fill="currentColor" d="M17.65 6.35A7.96 7.96 0 0 0 12 4a8 8 0 1 0 7.73 10h-2.08A6 6 0 1 1 12 6c1.66 0 3.14.69 4.22 1.78L13 11h7V4l-2.35 2.35z" /></svg>
        <span class="lbl">Refresh</span>
      </button>
      {/* Only shown inside the Android app, which can switch to another StreamISO server. */}
      <button id="server" type="button" title="Connect to another server" aria-label="Server" hidden={!p.onServer} onClick={() => p.onServer && p.onServer()}>
        <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true"><path fill="currentColor" d="M4 3h16a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zm0 10h16a1 1 0 0 1 1 1v6a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1v-6a1 1 0 0 1 1-1zm3-7.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zm0 10a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3z" /></svg>
        <span class="lbl">Server</span>
      </button>
    </header>
  );
}
