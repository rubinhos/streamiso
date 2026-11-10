// The editor of one disc: its details, cover and genres, a new online search, and the poster picker.
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import type { Disc } from "../shared/types";
import { api, errorText, json } from "../shared/api";
import { formatSize } from "../shared/format";
import { cap, cmpText, DEFAULT_GENRES, genreTag, groupSiblings, seasonOf, seenWord } from "./catalog";
import { Modal } from "./Modal";
import { PosterPicker } from "./PosterPicker";

interface Props {
  disc: Disc; // kept fresh by the library after each reload
  all: Disc[];
  knownGenres: string[];
  aliases: Record<string, string>;
  tmdbOn: boolean;
  reload(): Promise<void>;
  /** Closes the editor; saved tells the library to reload and focus the disc's card. */
  onClose(saved: boolean): void;
  onPicker(open: boolean): void;
}

function valueOf(v: number | string | null | undefined): string {
  return v !== undefined && v !== null ? String(v) : "";
}

function numOrNull(v: string): number | null {
  const t = v.trim();
  return t === "" ? null : Number(t);
}

export function Editor({ disc: d, all, knownGenres, aliases, tmdbOn, reload, onClose, onPicker }: Props) {
  const c = d.custom || {};
  const a = d.auto || ({} as Disc["auto"]);
  const [title, setTitle] = useState(c.title || "");
  const [sortTitle, setSortTitle] = useState(c.sort_title || "");
  const [year, setYear] = useState(valueOf(c.year));
  const [season, setSeason] = useState(valueOf(c.season));
  const [discNo, setDiscNo] = useState(valueOf(c.disc));
  const [genres, setGenres] = useState((c.genres || []).join(", "));
  const [synopsis, setSynopsis] = useState(c.synopsis || "");
  const [watched, setWatched] = useState(!!d.watched);
  const [fill, setFill] = useState(!!d.cover_fill);
  const [pendingFile, setPendingFile] = useState<File | null>(null);
  const [removeCover, setRemoveCover] = useState(false);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [query, setQuery] = useState("");
  const [lookupNote, setLookupNote] = useState("");
  const [searching, setSearching] = useState(false);
  const [picker, setPicker] = useState(false);
  const titleRef = useRef<HTMLInputElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const postersRef = useRef<HTMLButtonElement>(null);

  useEffect(() => titleRef.current?.focus(), []);
  useEffect(() => onPicker(picker), [picker]);

  // Escape closes the poster picker, or else the editor.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape") return;
      e.preventDefault();
      if (picker) closePicker();
      else onClose(false);
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [picker]);

  const pendingUrl = useMemo(() => (pendingFile ? URL.createObjectURL(pendingFile) : ""), [pendingFile]);

  const genreList = () => genres.split(",").map((s) => s.trim()).filter(Boolean);

  // The chips are named by their tag, so one is on for the same genre typed in English ("Comedy" for "Comédia").
  const chips = useMemo(() => {
    const seen: Record<string, boolean> = {};
    const names: string[] = [];
    for (const g of DEFAULT_GENRES.map((x) => genreTag(aliases, x)).concat(knownGenres)) {
      const k = g.toLowerCase();
      if (!seen[k]) {
        seen[k] = true;
        names.push(g);
      }
    }
    return names.sort(cmpText);
  }, [aliases, knownGenres]);
  const chosen = genreList().map((g) => genreTag(aliases, g).toLowerCase());

  function toggleChip(g: string) {
    const list = genreList();
    let idx = -1;
    list.forEach((x, i) => {
      if (genreTag(aliases, x).toLowerCase() === g.toLowerCase()) idx = i;
    });
    if (idx === -1) list.push(g);
    else list.splice(idx, 1);
    setGenres(list.join(", "));
  }

  function closePicker() {
    setPicker(false);
    setTimeout(() => postersRef.current?.focus(), 0);
  }

  async function applyCoverChange() {
    if (pendingFile) {
      const fd = new FormData();
      fd.append("cover", pendingFile);
      await api("/api/disc/" + d.id + "/cover", { method: "POST", body: fd });
    } else if (removeCover) {
      await api("/api/disc/" + d.id + "/cover", { method: "DELETE" });
    }
  }

  async function save(e: Event) {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      await api("/api/disc/" + d.id + "/meta", json("PUT", {
        title, sort_title: sortTitle, year: numOrNull(year), season: numOrNull(season), disc: numOrNull(discNo),
        genres: genreList(), synopsis, watched, cover_fill: fill,
      }));
      await applyCoverChange();
      onClose(true);
    } catch (err) {
      setError(errorText(err, "Could not save."));
      setSaving(false);
    }
  }

  async function reset() {
    if (!window.confirm("Remove all edits for this disc (including watched status and custom cover)?")) return;
    try {
      await api("/api/disc/" + d.id + "/meta", json("PUT", {}));
      await api("/api/disc/" + d.id + "/cover", { method: "DELETE" });
      onClose(true);
    } catch (err) {
      setError(errorText(err, "Could not save."));
    }
  }

  async function lookup() {
    setSearching(true);
    setLookupNote("");
    setError("");
    try {
      const res = await api<{ found: boolean; cover: boolean }>("/api/disc/" + d.id + "/lookup", json("POST", { query: query.trim() }));
      await reload();
      setLookupNote(!res.found
        ? "Nothing found. Try the name of the series or album alone."
        : !res.cover
          ? "Found the album and its details, but no cover art exists for it online. You can upload one with “Change cover”."
          : "Found. The cover and details were updated (your own edits still take priority).");
    } catch (err) {
      setError(errorText(err, "Could not save."));
    }
    setSearching(false);
  }

  const coverUrl = "/api/cover/" + d.id + "?v=" + d.cover_v;
  const coverNote = pendingFile ? "The new cover will be applied when you save."
    : removeCover ? "The custom cover will be removed when you save." : "";
  const siblings = groupSiblings(all, d);
  const s = seasonOf(d);

  return (
    <>
      <Modal id="editor" labelledBy="ed-heading" open={true} onClose={() => onClose(false)}>
        <form id="ed-form" class="panel" autocomplete="off" onSubmit={save}>
          <h2 id="ed-heading">{"Edit: " + d.title}</h2>
          <p id="ed-info" class="muted small">{d.rel + (d.label ? " · label: " + d.label : "") + " · " + formatSize(d.size)}</p>

          <div class="ed-grid">
            <div class={"ed-cover" + (d.kind === "cd" ? " cd" : "")}>
              <img id="ed-cover" alt="Current cover" class={(fill ? "fill" : "") + (removeCover && !pendingFile ? " faded" : "")} src={pendingUrl || coverUrl} />
              <label class="check fill-check"><input id="ed-fill" type="checkbox" checked={fill} onChange={(e) => setFill((e.target as HTMLInputElement).checked)} /> Fill the space</label>
              <label class="btn" for="ed-cover-file">Change cover</label>
              <input
                id="ed-cover-file"
                ref={fileRef}
                type="file"
                accept="image/jpeg,image/png,image/webp"
                hidden
                onChange={(e) => {
                  const f = (e.target as HTMLInputElement).files?.[0];
                  if (!f) return;
                  setPendingFile(f);
                  setRemoveCover(false);
                }}
              />
              <button type="button" id="ed-posters" ref={postersRef} hidden={d.kind === "cd" || !tmdbOn} onClick={() => setPicker(true)}>Choose online poster</button>
              <button
                type="button"
                id="ed-cover-remove"
                hidden={!((d.custom_cover && !removeCover) || pendingFile)}
                onClick={() => {
                  if (pendingFile) {
                    setPendingFile(null);
                    if (fileRef.current) fileRef.current.value = "";
                  } else if (d.custom_cover) setRemoveCover(true);
                }}
              >Remove custom cover</button>
              <p id="ed-cover-note" class="muted small" hidden={!coverNote}>{coverNote}</p>
            </div>

            <div class="ed-fields">
              <label>Title
                <input id="ed-title" ref={titleRef} type="text" maxLength={200} value={title} placeholder={a.title || ""} onInput={(e) => setTitle((e.target as HTMLInputElement).value)} />
              </label>
              <label>Sort as <span class="muted small">(empty: by the title)</span>
                {/* an empty sort title sorts by the title: show which one */}
                <input id="ed-sort" type="text" maxLength={200} value={sortTitle} placeholder={title.trim() || a.title || ""} onInput={(e) => setSortTitle((e.target as HTMLInputElement).value)} />
              </label>
              <div class="row">
                <label>Year
                  <input id="ed-year" type="number" min="1888" max="2100" value={year} placeholder={valueOf(a.year)} onInput={(e) => setYear((e.target as HTMLInputElement).value)} />
                </label>
                <label>Season
                  <input id="ed-season" type="number" min="0" max="999" value={season} onInput={(e) => setSeason((e.target as HTMLInputElement).value)} />
                </label>
                <label>Disc
                  <input id="ed-disc" type="number" min="1" max="999" value={discNo} onInput={(e) => setDiscNo((e.target as HTMLInputElement).value)} />
                </label>
              </div>
              <label>Genres <span class="muted small">(comma-separated, or click below)</span>
                <input id="ed-genres" type="text" value={genres} placeholder={(a.genres || []).join(", ")} onInput={(e) => setGenres((e.target as HTMLInputElement).value)} />
              </label>
              <div id="ed-chips" class="chips">
                {chips.map((g) => (
                  <button key={g} type="button" class="chip" aria-pressed={chosen.indexOf(g.toLowerCase()) !== -1 ? "true" : "false"} onClick={() => toggleChip(g)}>{g}</button>
                ))}
              </div>
              <label>Synopsis
                <textarea id="ed-synopsis" rows={5} maxLength={4000} value={synopsis} placeholder={a.synopsis || ""} onInput={(e) => setSynopsis((e.target as HTMLTextAreaElement).value)} />
              </label>
              <div class="lookup">
                <label class="grow">Search online as
                  <input id="ed-query" type="text" maxLength={200} placeholder="Name of the series, film or album" value={query} onInput={(e) => setQuery((e.target as HTMLInputElement).value)} />
                </label>
                <button type="button" id="ed-lookup" disabled={searching} onClick={lookup}>{searching ? "Searching…" : "Search again"}</button>
              </div>
              <p id="ed-lookup-note" class="muted small" hidden={!lookupNote}>{lookupNote}</p>
              <label class="check"><input id="ed-watched" type="checkbox" checked={watched} onChange={(e) => setWatched((e.target as HTMLInputElement).checked)} /> <span id="ed-watched-label">{cap(seenWord(d))}</span></label>
            </div>
          </div>

          <p id="ed-error" class="error" hidden={!error}>{error}</p>
          <div class="actions">
            <button type="button" id="ed-reset" class="danger" onClick={reset}>Clear edits</button>
            <div class="spacer" />
            <button type="button" id="ed-cancel" onClick={() => onClose(false)}>Cancel</button>
            <button type="submit" id="ed-save" class="primary" disabled={saving}>Save</button>
          </div>
        </form>
      </Modal>
      {picker && (
        <PosterPicker
          disc={d}
          siblings={siblings}
          groupWord={s && s.season === null ? "film" : "season"}
          initialQuery={query.trim()}
          reload={reload}
          onSaved={(covered) => setLookupNote(covered
            ? "Poster saved, but this disc’s own cover is still shown first. Remove it to see the poster."
            : "Poster saved.")}
          onClose={closePicker}
        />
      )}
    </>
  );
}
