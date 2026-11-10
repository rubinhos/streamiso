// The poster picker (TMDB): the posters of the current match, the other titles found, and "All posters"
// of one of them. A poster from another title also makes that title the disc's match.
import { useEffect, useRef, useState } from "preact/hooks";
import type { Disc, PosterChoices } from "../shared/types";
import { api, errorText, json } from "../shared/api";
import { focusQuietly } from "../shared/format";
import { Modal } from "./Modal";

interface PickItem {
  poster_path: string;
  tmdb_kind: string;
  tmdb_id: number;
  current?: boolean;
  /** Another title: its name; the cell also offers all of its posters. */
  caption?: string;
}

interface PickSection {
  key: string;
  heading: string;
  items: PickItem[];
  thumbUrl: string;
}

interface Props {
  disc: Disc;
  siblings: Disc[];
  groupWord: "film" | "season";
  initialQuery: string;
  reload(): Promise<void>;
  onSaved(covered: boolean): void;
  onClose(): void;
}

export function PosterPicker({ disc: d, siblings, groupWord, initialQuery, reload, onSaved, onClose }: Props) {
  const [query, setQuery] = useState(initialQuery);
  const [placeholder, setPlaceholder] = useState("");
  const [note, setNote] = useState({ text: "", error: false });
  const [sections, setSections] = useState<PickSection[]>([]);
  const [all, setAll] = useState(siblings.length > 0);
  const [busy, setBusy] = useState(false);
  const [loadingMore, setLoadingMore] = useState<Record<string, boolean>>({});
  const [doneMore, setDoneMore] = useState<Record<string, boolean>>({});
  const seq = useRef(0);
  const bodyRef = useRef<HTMLDivElement>(null);
  const queryRef = useRef<HTMLInputElement>(null);
  const modalRef = useRef<HTMLDivElement>(null);
  // after a render: the section to scroll to and focus, or "first" for the best poster of a new search
  const focusNext = useRef<string | null>(null);

  useEffect(() => {
    if (modalRef.current) modalRef.current.scrollTop = 0;
    search();
    return () => {
      seq.current++;
    };
  }, []);

  useEffect(() => {
    const target = focusNext.current;
    const body = bodyRef.current;
    if (!target || !body) return;
    focusNext.current = null;
    if (target === "first") {
      const first = body.querySelector<HTMLElement>(".pk-thumb.current") || body.querySelector<HTMLElement>(".pk-thumb");
      if (first && document.activeElement !== queryRef.current) first.focus();
      return;
    }
    const sec = body.querySelector<HTMLElement>('section[data-key="' + target + '"]');
    if (!sec) return;
    sec.scrollIntoView({ block: "start", behavior: "smooth" });
    focusQuietly(sec.querySelector<HTMLElement>(".pk-thumb"));
  });

  async function search() {
    const mine = ++seq.current;
    setSections([]);
    setDoneMore({});
    setNote({ text: "Searching…", error: false });
    const q = query.trim();
    try {
      const res = await api<PosterChoices>("/api/disc/" + d.id + "/posters" + (q ? "?query=" + encodeURIComponent(q) : ""));
      if (mine !== seq.current) return;
      if (!q) setPlaceholder(res.query || "");
      const found: PickSection[] = [];
      const cur = res.current;
      if (cur && cur.posters.length) {
        const label = (cur.title || "Current match") + (cur.year ? " (" + cur.year + ")" : "") +
          (cur.season !== null && cur.season !== undefined ? " – Season " + cur.season : "");
        found.push({
          key: "current", heading: "Posters of " + label, thumbUrl: res.thumb_url,
          items: cur.posters.map((p) => ({ poster_path: p, tmdb_kind: cur.tmdb_kind, tmdb_id: cur.tmdb_id, current: p === cur.poster_path })),
        });
      }
      const others = res.matches.filter((m) => !cur || m.tmdb_kind !== cur.tmdb_kind || m.tmdb_id !== cur.tmdb_id);
      if (others.length) {
        found.push({
          key: "titles", heading: cur ? "Other titles found" : "Titles found", thumbUrl: res.thumb_url,
          items: others.map((m) => ({
            poster_path: m.poster_path, tmdb_kind: m.tmdb_kind, tmdb_id: m.tmdb_id,
            caption: m.title + (m.year ? " (" + m.year + ")" : "") + (m.tmdb_kind === "tv" ? " · series" : ""),
          })),
        });
      }
      const notes = [found.length
        ? "Pick a poster. One from another title also takes that title’s details."
        : "No posters found. Try another name, e.g. the film alone or “Name (1997)”."];
      if (res.covered) notes.push("This disc has its own cover, which is still shown first: remove it in the editor to see the poster.");
      setNote({ text: notes.join(" "), error: false });
      focusNext.current = "first";
      setSections(found);
    } catch (err) {
      if (mine !== seq.current) return;
      setNote({ text: errorText(err, "Search failed."), error: true });
    }
  }

  // Shows every poster of another title in a section of its own, above the list of titles.
  async function showAllPosters(it: PickItem) {
    const mine = seq.current;
    const id = it.tmdb_kind + ":" + it.tmdb_id;
    setLoadingMore((m) => ({ ...m, [id]: true }));
    try {
      const res = await api<{ posters: string[]; thumb_url: string }>(
        "/api/disc/" + d.id + "/posters?tmdb_kind=" + it.tmdb_kind + "&tmdb_id=" + it.tmdb_id);
      if (mine !== seq.current) return;
      setDoneMore((m) => ({ ...m, [id]: true }));
      if (!res.posters.length) return;
      const key = "all-" + id;
      focusNext.current = key;
      setSections((secs) => {
        const at = secs.findIndex((s) => s.key === "titles");
        const sec: PickSection = {
          key, heading: "Posters of " + it.caption, thumbUrl: res.thumb_url,
          items: res.posters.map((p) => ({ poster_path: p, tmdb_kind: it.tmdb_kind, tmdb_id: it.tmdb_id })),
        };
        return [...secs.slice(0, at), sec, ...secs.slice(at)];
      });
    } catch (err) {
      setNote({ text: errorText(err, "Could not load the posters."), error: true });
    }
    setLoadingMore((m) => ({ ...m, [id]: false }));
  }

  async function choose(it: PickItem) {
    const targets = [d].concat(all && siblings.length ? siblings : []);
    const body = { poster_path: it.poster_path, tmdb_kind: it.tmdb_kind, tmdb_id: it.tmdb_id };
    setNote({ text: "Saving the poster…", error: false });
    setBusy(true);
    let covered = false;
    try {
      for (const t of targets) {
        const res = await api<{ covered: boolean }>("/api/disc/" + t.id + "/poster", json("POST", body));
        covered = covered || (t === d && res.covered);
      }
      await reload();
      onSaved(covered);
      onClose();
    } catch (err) {
      setNote({ text: errorText(err, "Could not save the poster."), error: true });
      setBusy(false);
    }
  }

  const n = siblings.length;
  return (
    <Modal id="posters" labelledBy="pk-heading" open={true} onClose={onClose} modalRef={modalRef}>
      <div class="panel picker-panel">
        <div class="season-head">
          <h2 id="pk-heading">{"Choose a poster: " + d.title}</h2>
          <div class="spacer" />
          <button type="button" id="pk-close" title="Close" aria-label="Close" onClick={onClose}>&#x2715;</button>
        </div>
        <div class="lookup">
          <label class="grow">Search for
            <input
              id="pk-query"
              ref={queryRef}
              type="text"
              maxLength={200}
              value={query}
              placeholder={placeholder}
              onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  search();
                }
              }}
            />
          </label>
          <button type="button" id="pk-search" onClick={search}>Search</button>
        </div>
        <label id="pk-all-row" class="check" hidden={n === 0}>
          <input id="pk-all" type="checkbox" checked={all} onChange={(e) => setAll((e.target as HTMLInputElement).checked)} />{" "}
          <span id="pk-all-label">{n === 0 ? "" : "Also use it for the " + (n === 1 ? "other disc" : n + " other discs") + " of this " + groupWord}</span>
        </label>
        <p id="pk-note" class={note.error ? "error" : "muted small"}>{note.text}</p>
        <div id="pk-body" ref={bodyRef}>
          {sections.map((sec) => (
            <section key={sec.key} data-key={sec.key}>
              <h3>{sec.heading}</h3>
              <div class="pk-grid">
                {sec.items.map((it) => {
                  const thumb = (key?: string) => (
                    <button key={key} type="button" class={"pk-thumb" + (it.current ? " current" : "")} title={it.caption || "Use this poster"} disabled={busy} onClick={() => choose(it)}>
                      <img alt={it.caption || "Poster"} loading="lazy" src={sec.thumbUrl + it.poster_path} />
                      {it.caption && <span class="pk-caption">{it.caption}</span>}
                    </button>
                  );
                  if (!it.caption) return thumb(it.poster_path);
                  const id = it.tmdb_kind + ":" + it.tmdb_id;
                  return (
                    <div key={id} class="pk-cell">
                      {thumb()}
                      {!doneMore[id] && (
                        <button type="button" class="pk-more" disabled={busy || loadingMore[id]} onClick={() => showAllPosters(it)}>
                          {loadingMore[id] ? "Loading…" : "All posters"}
                        </button>
                      )}
                    </div>
                  );
                })}
              </div>
            </section>
          ))}
        </div>
      </div>
    </Modal>
  );
}
