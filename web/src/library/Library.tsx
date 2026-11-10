// The library page: the discs as cards, the filters on top, and the windows (a season's discs, the editor).
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import type { Disc, LibraryAnswer } from "../shared/types";
import { api, json } from "../shared/api";
import { focusQuietly } from "../shared/format";
import type { DiscActions } from "./Cards";
import { DiscItem, GroupItem } from "./Cards";
import type { Prefs } from "./catalog";
import { byDiscNumber, discCount, isGroup, layout, seasonOf, seenLabels, visibleList } from "./catalog";
import { Editor } from "./Editor";
import { Header } from "./Header";
import { focusCard, focusGroup, move } from "./navigation";
import { loadPrefs, markPlaying, savePrefs, stopIfWeStartedIt } from "./prefs";
import { SeasonDialog } from "./SeasonDialog";

interface Data {
  discs: Disc[];
  genres: string[];
  aliases: Record<string, string>;
  mediaDir: string;
  tmdb: boolean;
}

declare global {
  interface Window {
    /** Added by the Android app to its WebView. */
    StreamIsoAndroid?: { changeServer?: () => void };
  }
}

export function Library() {
  const [data, setData] = useState<Data | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [prefs, setPrefs] = useState<Prefs>(loadPrefs);
  const [filter, setFilter] = useState("");
  const [seasonKey, setSeasonKey] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [opening, setOpening] = useState<string | null>(null);
  const [rescanning, setRescanning] = useState(false);

  const gridRef = useRef<HTMLDivElement>(null);
  const filterRef = useRef<HTMLInputElement>(null);
  const seasonCardsRef = useRef<HTMLDivElement>(null);
  const seasonCloseRef = useRef<HTMLButtonElement>(null);
  const seasonModalRef = useRef<HTMLDivElement>(null);
  const firstLoad = useRef(true);
  // after the next render: what gets the focus
  const focusNext = useRef<{ id?: string; group?: string; seasonStart?: boolean } | null>(null);

  const discs = data ? data.discs : [];
  const aliases = data ? data.aliases : {};
  const editing = editingId ? discs.find((d) => d.id === editingId) || null : null;

  async function load(): Promise<void> {
    try {
      const res = await api<LibraryAnswer>("/api/discs");
      setLoadFailed(false);
      setData({ discs: res.discs, genres: res.genres, aliases: res.genre_aliases || {}, mediaDir: res.media_dir, tmdb: !!res.tmdb });
      setPrefs((p) => {
        if (p.genre && res.genres.indexOf(p.genre) === -1) {
          const next = { ...p, genre: "" }; // the filtered genre no longer exists
          savePrefs(next);
          return next;
        }
        return p;
      });
    } catch {
      setLoadFailed(true);
    }
  }

  useEffect(() => {
    stopIfWeStartedIt();
    load();
    // When returning through the browser's history the page can reappear without reloading.
    const onShow = () => {
      setOpening(null);
      stopIfWeStartedIt();
    };
    window.addEventListener("pageshow", onShow);
    return () => window.removeEventListener("pageshow", onShow);
  }, []);

  function changePrefs(change: Partial<Prefs>) {
    setPrefs((p) => {
      const next = { ...p, ...change };
      savePrefs(next);
      return next;
    });
  }

  // ------------------------------------------------------------------ what is shown
  const view = useMemo(() => layout(discs, prefs, filter, aliases), [discs, prefs, filter, aliases]);
  const seasonDiscs = useMemo(() => {
    if (!seasonKey) return [];
    return visibleList(discs, prefs, filter, aliases).filter((d) => {
      const s = seasonOf(d);
      return s && s.key === seasonKey;
    }).sort(byDiscNumber);
  }, [seasonKey, discs, prefs, filter, aliases]);
  const seasonOpen = seasonKey !== null && seasonDiscs.length > 0;

  // A season with no disc left (filtered out, or all edited away) closes.
  useEffect(() => {
    if (seasonKey && !seasonDiscs.length) setSeasonKey(null);
  }, [seasonKey, seasonDiscs.length]);

  // While a window is open, only it scrolls: the page behind would add a second scroll bar.
  useEffect(() => {
    document.documentElement.classList.toggle("modal-open", !!editing || seasonOpen || pickerOpen);
  }, [editing, seasonOpen, pickerOpen]);

  useEffect(() => {
    const f = focusNext.current;
    if (f) {
      focusNext.current = null;
      if (f.seasonStart) {
        if (seasonModalRef.current) seasonModalRef.current.scrollTop = 0;
        seasonCardsRef.current?.querySelector<HTMLElement>(".card")?.focus();
      } else if (f.group) {
        focusGroup(gridRef.current, f.group);
      } else if (f.id) {
        focusCard(seasonOpen ? seasonCardsRef.current : gridRef.current, f.id);
      }
    }
    if (data && firstLoad.current) {
      firstLoad.current = false;
      if (!editing && !seasonOpen) gridRef.current?.querySelector<HTMLElement>(".card")?.focus();
    }
  });

  // ------------------------------------------------------------------ actions
  function updateDisc(id: string, change: Partial<Disc>) {
    setData((dt) => dt && { ...dt, discs: dt.discs.map((d) => (d.id === id ? { ...d, ...change } : d)) });
  }

  const actions: DiscActions = {
    play(d, resume) {
      setOpening(d.title);
      api<{ player_url: string }>("/api/play/" + d.id, json("POST", { resume: !!resume })).then((res) => {
        markPlaying();
        // Give VLC a moment to start before opening the stream.
        setTimeout(() => { window.location.href = res.player_url; }, 1200);
      }, () => {
        setOpening(null);
        alert("Could not open the disc.");
      });
    },
    toggleWatched(d) {
      const want = !d.watched;
      updateDisc(d.id, { watched: want });
      api("/api/disc/" + d.id + "/watched", json("POST", { watched: want })).then(null, () => {
        updateDisc(d.id, { watched: !want });
        alert("Could not save. Please try again.");
      });
    },
    edit(d) {
      setEditingId(d.id);
    },
    // Forgets where the disc was left: it leaves "Continue watching" and next time starts over.
    forgetResume(d) {
      updateDisc(d.id, { resume: null });
      api("/api/disc/" + d.id + "/resume/clear", { method: "POST" }).then(load, load);
    },
  };

  function openSeason(key: string) {
    focusNext.current = { seasonStart: true };
    setSeasonKey(key);
  }

  function closeSeason() {
    if (seasonKey) focusNext.current = { group: seasonKey };
    setSeasonKey(null);
  }

  async function closeEditor(saved: boolean) {
    const id = editingId;
    setEditingId(null);
    setPickerOpen(false);
    if (saved) await load();
    if (id) focusNext.current = { id };
  }

  function rescan() {
    setRescanning(true);
    api("/api/rescan", { method: "POST" }).then(load).then(() => setRescanning(false), () => setRescanning(false));
  }

  // ------------------------------------------------------------------ keyboard (TV remote)
  const latest = useRef({ editing, seasonOpen, discs, actions, closeSeason });
  latest.current = { editing, seasonOpen, discs, actions, closeSeason };

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const st = latest.current;
      if (st.editing) return; // the editor handles its own keys
      if (st.seasonOpen && e.key === "Escape") {
        e.preventDefault();
        st.closeSeason();
        return;
      }
      const t = e.target as HTMLElement | null;
      const tag = t && t.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") {
        if (t === filterRef.current && e.key === "ArrowDown") {
          e.preventDefault();
          gridRef.current?.querySelector<HTMLElement>(".card")?.focus();
        }
        return;
      }
      const itemEl = t && t.closest ? (t.closest(".item") as HTMLElement | null) : null;
      const disc = itemEl && itemEl.dataset.id ? st.discs.find((d) => d.id === itemEl.dataset.id) : undefined;
      if (itemEl && (e.key === "e" || e.key === "E")) {
        e.preventDefault();
        if (disc) st.actions.edit(disc);
        return;
      }
      if (itemEl && (e.key === "w" || e.key === "W")) {
        e.preventDefault();
        if (disc) st.actions.toggleWatched(disc);
        return;
      }
      if (e.key && e.key.indexOf("Arrow") === 0) {
        e.preventDefault();
        const key = e.key as "ArrowUp" | "ArrowDown" | "ArrowLeft" | "ArrowRight";
        if (st.seasonOpen) {
          // top row of the season window: go up to its close button
          if (seasonCardsRef.current && !move(seasonCardsRef.current, key) && key === "ArrowUp" && itemEl) seasonCloseRef.current?.focus();
        } else if (gridRef.current) {
          // top of the grid: go up to the filter box
          if (!move(gridRef.current, key) && key === "ArrowUp" && itemEl) focusQuietly(filterRef.current);
        }
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);

  const android = window.StreamIsoAndroid;
  const onServer = android && typeof android.changeServer === "function" ? () => android.changeServer!() : null;

  // ------------------------------------------------------------------ page
  let emptyMessage: any = null;
  if (loadFailed) emptyMessage = "Could not load the library. Is the service running?";
  else if (view.empty === "none" && data) {
    emptyMessage = (
      <>
        Nothing found. Put DVD <code>.iso</code> files in your DVD folder and audio CD <code>.cue</code>/<code>.bin</code> files in
        your CD folder, then click <b>Refresh</b>. Folders in use (inside the container): <code>{data.mediaDir}</code>
      </>
    );
  } else if (view.empty === "no-match") emptyMessage = "Nothing matches these filters.";
  else if (view.empty === "all-seen") emptyMessage = "Everything here has been played already. Click the eye button to show it.";

  return (
    <>
      <Header
        count={view.count}
        prefs={prefs}
        genres={data ? data.genres : []}
        seen={seenLabels(discs, prefs)}
        filter={filter}
        filterRef={filterRef}
        rescanning={rescanning}
        onFilter={setFilter}
        onPrefs={changePrefs}
        onRescan={rescan}
        onServer={onServer}
      />

      <main>
        <div id="grid" ref={gridRef}>
          {view.sections.map((sec) => (
            <section key={sec.progress ? "progress" : "kind:" + (isGroup(sec.items[0]) ? "dvd" : sec.items[0].kind)} class={"group" + (sec.progress ? " progress" : "")}>
              {sec.title && <h2>{sec.title}<span class="count">{String(discCount(sec.items))}</span></h2>}
              <div class="cards" role="list">
                {sec.items.map((it) => (isGroup(it)
                  ? <GroupItem key={"g:" + it.key} g={it} aliases={aliases} open={openSeason} />
                  : <DiscItem key={it.id} d={it} aliases={aliases} actions={actions} inProgress={sec.progress} />))}
              </div>
            </section>
          ))}
        </div>
        <p id="empty" class="muted" hidden={!emptyMessage}>{emptyMessage}</p>
      </main>

      <div id="opening" hidden={opening === null}>
        <div>
          <div class="spinner" />
          <p id="opening-title">{opening}</p>
          <p class="muted">Opening disc…</p>
        </div>
      </div>

      <SeasonDialog
        discs={seasonOpen ? seasonDiscs : []}
        aliases={aliases}
        actions={actions}
        cardsRef={seasonCardsRef}
        closeRef={seasonCloseRef}
        modalRef={seasonModalRef}
        onClose={closeSeason}
      />

      {editing && data && (
        <Editor
          key={editing.id}
          disc={editing}
          all={discs}
          knownGenres={data.genres}
          aliases={aliases}
          tmdbOn={data.tmdb}
          reload={load}
          onClose={closeEditor}
          onPicker={setPickerOpen}
        />
      )}
    </>
  );
}
